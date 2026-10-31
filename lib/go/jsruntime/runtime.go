// Package jsruntime executes synchronous JavaScript in a fresh Goja VM. It is a
// cooperative boundary: callers must supply process/container limits and make
// every native callback honor the context. No Node or reflection bridge is added.
package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

type Limits struct {
	Timeout                                                      time.Duration
	SourceBytes, InputBytes, ResultBytes, LogBytes, Depth, Nodes int
}

func DefaultLimits() Limits {
	return Limits{Timeout: 5 * time.Second, SourceBytes: 65536, InputBytes: 65536, ResultBytes: 262144, LogBytes: 16384, Depth: 32, Nodes: 10000}
}

type VM struct {
	Runtime *goja.Runtime
	ctx     context.Context
	handles map[*goja.Object]json.RawMessage
}

func (v *VM) Context() context.Context { return v.ctx }

// RegisterHandle allows the encoder to emit a fixed stub without invoking user
// methods or recursively exporting an object with host callbacks.
func (v *VM) RegisterHandle(o *goja.Object, stub any) error {
	b, e := json.Marshal(stub)
	if e == nil {
		v.handles[o] = b
	}
	return e
}
func (v *VM) Function(o *goja.Object, name string, fn func(goja.FunctionCall) goja.Value) error {
	return o.DefineDataProperty(name, v.Runtime.ToValue(fn), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
}
func (v *VM) Throw(err error) { panic(v.Runtime.NewGoError(err)) }

type Result struct {
	JSON        json.RawMessage
	Diagnostics string
}

func Run(ctx context.Context, source string, input json.RawMessage, limits Limits, bind func(*VM) error) (out Result, err error) {
	defaults := DefaultLimits()
	if limits.Timeout <= 0 || limits.Timeout > defaults.Timeout || limits.SourceBytes <= 0 || limits.SourceBytes > defaults.SourceBytes || limits.InputBytes <= 0 || limits.InputBytes > defaults.InputBytes || limits.ResultBytes <= 0 || limits.ResultBytes > defaults.ResultBytes || limits.LogBytes <= 0 || limits.LogBytes > defaults.LogBytes || limits.Depth <= 0 || limits.Depth > defaults.Depth || limits.Nodes <= 0 || limits.Nodes > defaults.Nodes {
		return out, &Error{"RESOURCE_LIMIT_EXCEEDED", "Invalid execution limits"}
	}
	if len(source) > limits.SourceBytes || len(input) > limits.InputBytes || !json.Valid(input) {
		return out, &Error{"RESOURCE_LIMIT_EXCEEDED", "Script or input exceeds limits"}
	}
	ctx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()
	r := goja.New()
	r.SetMaxCallStackSize(512)
	v := &VM{Runtime: r, ctx: ctx, handles: map[*goja.Object]json.RawMessage{}}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-ctx.Done():
			r.Interrupt("execution deadline")
		case <-done:
		}
	}()
	defer func() {
		close(done)
		wg.Wait()
		if p := recover(); p != nil {
			err = &Error{"SCRIPT_RUNTIME_ERROR", "Script execution failed"}
		}
	}()
	// Capture pristine intrinsics before any untrusted code runs. Serialization
	// stays in the VM with the watchdog active; no arbitrary Value.Export/ToJSON.
	parserValue, e := r.RunString("(function(parse){return function(s){return parse(s)}})(JSON.parse)")
	if e != nil {
		return out, e
	}
	parseInput, _ := goja.AssertFunction(parserValue)
	encoder, e := r.RunString(encoderSource)
	if e != nil {
		return out, e
	}
	encode, _ := goja.AssertFunction(encoder)
	stub := func(call goja.FunctionCall) goja.Value {
		if obj, ok := call.Argument(0).(*goja.Object); ok {
			if b, ok := v.handles[obj]; ok {
				return r.ToValue(string(b))
			}
		}
		return goja.Null()
	}
	logs := make([]byte, 0)
	console := r.NewObject()
	_ = v.Function(console, "log", func(call goja.FunctionCall) goja.Value {
		// Logging accepts primitives only; it never invokes object ToString/getters.
		for _, a := range call.Arguments {
			if _, ok := a.(*goja.Object); ok {
				logs = append(logs, []byte("[object]")...)
			} else if !goja.IsUndefined(a) && !goja.IsNull(a) {
				s := a.String()
				if len(s) > limits.LogBytes-len(logs) {
					v.Throw(&Error{"RESOURCE_LIMIT_EXCEEDED", "Diagnostic log limit exceeded"})
				}
				logs = append(logs, s...)
			}
			logs = append(logs, ' ')
			if len(logs) > limits.LogBytes {
				v.Throw(&Error{"RESOURCE_LIMIT_EXCEEDED", "Diagnostic log limit exceeded"})
			}
		}
		logs = append(logs, '\n')
		return goja.Undefined()
	})
	_ = r.Set("console", console)
	if bind != nil {
		if e = bind(v); e != nil {
			return out, e
		}
	}
	program, e := goja.Compile("memory-code.js", source, true)
	if e != nil {
		return out, &Error{"SCRIPT_SYNTAX_ERROR", "Invalid JavaScript source"}
	}
	if _, e = r.RunProgram(program); e != nil {
		return out, classify(e, ctx)
	}
	main, ok := goja.AssertFunction(r.Get("main"))
	if !ok {
		return out, &Error{"SCRIPT_RUNTIME_ERROR", "Source must define synchronous function main(input)"}
	}
	// JSON.parse is captured separately before calling main, through the trusted
	// encoder factory, so user replacements of global JSON do not affect input.
	in, e := parseInput(goja.Undefined(), r.ToValue(string(input)))
	if e != nil {
		return out, classify(e, ctx)
	}
	value, e := main(goja.Undefined(), in)
	if e != nil {
		return out, classify(e, ctx)
	}
	encoded, e := encode(goja.Undefined(), value, r.ToValue(stub), r.ToValue(limits.Depth), r.ToValue(limits.Nodes), r.ToValue(limits.ResultBytes))
	if e != nil {
		return out, classifyResult(e, ctx)
	}
	b := []byte(encoded.String())
	if len(b) > limits.ResultBytes {
		return out, &Error{"RESOURCE_LIMIT_EXCEEDED", "Structured result exceeds limit"}
	}
	if ctx.Err() != nil {
		return out, &Error{"EXECUTION_TIMEOUT", "Execution deadline exceeded"}
	}
	out.JSON = b
	out.Diagnostics = string(logs)
	return out, nil
}
func classify(e error, ctx context.Context) error {
	if ctx.Err() != nil {
		return &Error{"EXECUTION_TIMEOUT", "Execution deadline exceeded"}
	}
	var interrupted *goja.InterruptedError
	if errors.As(e, &interrupted) {
		return &Error{"EXECUTION_TIMEOUT", "Execution interrupted"}
	}
	var ex *goja.Exception
	if errors.As(e, &ex) {
		return &Error{"SCRIPT_RUNTIME_ERROR", "JavaScript or host call failed"}
	}
	return &Error{"SCRIPT_RUNTIME_ERROR", fmt.Sprintf("JavaScript execution failed (%T)", e)}
}
func classifyResult(e error, ctx context.Context) error {
	if ctx.Err() != nil {
		return &Error{"EXECUTION_TIMEOUT", "Execution deadline exceeded"}
	}
	return &Error{"UNSUPPORTED_RESULT", "Result has unsupported values, accessors, cycles, prototype or size"}
}

const encoderSource = `(function(){
 const keys=Reflect.ownKeys, desc=Object.getOwnPropertyDescriptor, proto=Object.getPrototypeOf, create=Object.create, setProto=Object.setPrototypeOf;
 const array=Array.isArray, objectProto=Object.prototype, arrayProto=Array.prototype, stringify=JSON.stringify, parse=JSON.parse, own=Object.prototype.hasOwnProperty, finite=Number.isFinite;
 const min=-9223372036854775808n,max=9223372036854775807n, bigString=BigInt.prototype.toString, apply=Reflect.apply;
 return function(value,stub,maxDepth,maxNodes,maxBytes){
  let count=0,bytes=0; const seen=[];setProto(seen,null);
  function encode(v,depth){
   if (++count>maxNodes||depth>maxDepth) throw Error('limit');
   const t=typeof v;
   if (v===null||t==='boolean') return v;
   if (t==='string'){bytes+=v.length*3+2;if(bytes>maxBytes)throw Error('limit');return v;}
   if (t==='number'){if(!finite(v))throw Error('number');return v;}
   if (t==='bigint'){if(v<min||v>max)throw Error('bigint');const o=create(null);o.type='integer';o.value=apply(bigString,v,[]);return o;}
   if(t!=='object')throw Error('unsupported');
   const fixed=stub(v);if(fixed!==null){return encode(parse(fixed),depth+1);}
   const isArray=array(v),p=proto(v);if(p!==null&&p!==objectProto&&!(isArray&&p===arrayProto))throw Error('prototype');
   for(let i=0;i<seen.length;i++)if(seen[i]===v)throw Error('cycle');seen[seen.length]=v;
   const out=isArray?[]:create(null);if(isArray)setProto(out,null);
   const names=keys(v);let length=0;if(isArray){const d=desc(v,'length');if(!d||!apply(own,d,['value'])||d.value>maxNodes)throw Error('array');length=d.value;}
   for(let i=0;i<names.length;i++){
    const k=names[i];if(typeof k!=='string')throw Error('symbol');if(isArray&&k==='length')continue;
    const d=desc(v,k);if(!d||!apply(own,d,['value']))throw Error('accessor');
    if(isArray&&(!/^(0|[1-9][0-9]*)$/.test(k)||Number(k)>=length))throw Error('array member');
    bytes+=k.length*3+3;if(bytes>maxBytes)throw Error('limit');out[k]=encode(d.value,depth+1);
   }
   if(isArray){for(let i=0;i<length;i++)if(!desc(out,''+i))throw Error('sparse array');}
   seen.length--;return out;
  }
  return stringify(encode(value,0));
 };
})()`
