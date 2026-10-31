package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func TestResultBoundary(t *testing.T) {
	tests := []struct {
		source, want string
		bad          bool
	}{
		{`function main(){return {value:9223372036854775807n,small:1n,zero:0,empty:"",no:false}}`, `{"value":{"type":"integer","value":"9223372036854775807"},"small":{"type":"integer","value":"1"},"zero":0,"empty":"","no":false}`, false},
		{`function main(){return 9223372036854775808n}`, "", true},
		{`function main(){return undefined}`, "", true},
		{`function main(){return {x:undefined}}`, "", true},
		{`function main(){return [undefined]}`, "", true},
		{`function main(){const a={};a.x=a;return a}`, "", true},
		{`function main(){return {get x(){throw Error('must not invoke')}}}`, "", true},
		{`function main(){Object.prototype.value=42;return {get x(){return 3}}}`, "", true},
		{`function main(){return Promise.resolve(1)}`, "", true},
		{`function main(){return new Date()}`, "", true},
		{`function main(){return Infinity}`, "", true},
		{`function main(){return Symbol()}`, "", true},
		{`function main(){return function(){}}`, "", true},
		{`function main(){const a=[];a.length=1;return a}`, "", true},
		{`function main(){return {toJSON(){return 1}}}`, "", true},
	}
	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			out, e := Run(t.Context(), test.source, json.RawMessage(`{}`), DefaultLimits(), nil)
			if test.bad {
				var x *Error
				if !errors.As(e, &x) || x.Code != "UNSUPPORTED_RESULT" {
					t.Fatalf("expected result rejection, got %v", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if string(out.JSON) != test.want {
				t.Fatalf("%s", out.JSON)
			}
		})
	}
}
func TestTimeoutAndRecovery(t *testing.T) {
	for _, source := range []string{`function main(){while(true){}}`, `function main(){return new Proxy({}, {ownKeys(){while(true){}}})}`} {
		l := DefaultLimits()
		l.Timeout = 20 * time.Millisecond
		_, e := Run(t.Context(), source, json.RawMessage(`{}`), l, nil)
		var x *Error
		if !errors.As(e, &x) || x.Code != "EXECUTION_TIMEOUT" {
			t.Fatal(e)
		}
	}
	out, e := Run(t.Context(), `function main(input){return input}`, json.RawMessage(`{"ok":true}`), DefaultLimits(), nil)
	if e != nil || string(out.JSON) != `{"ok":true}` {
		t.Fatal(out, e)
	}
}
func TestNativeCancellationAndHandle(t *testing.T) {
	l := DefaultLimits()
	l.Timeout = 20 * time.Millisecond
	_, e := Run(t.Context(), `function main(){return wait()}`, json.RawMessage(`null`), l, func(v *VM) error {
		return v.Runtime.Set("wait", func(goja.FunctionCall) goja.Value { <-v.Context().Done(); v.Throw(v.Context().Err()); return nil })
	})
	var x *Error
	if !errors.As(e, &x) || x.Code != "EXECUTION_TIMEOUT" {
		t.Fatal(e)
	}
	out, e := Run(context.Background(), `function main(){JSON.parse=()=>({bad:true});Object.prototype.toJSON=()=>"bad";return handle}`, json.RawMessage(`null`), DefaultLimits(), func(v *VM) error {
		o := v.Runtime.NewObject()
		_ = v.Function(o, "secret", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
		_ = v.RegisterHandle(o, map[string]string{"id": "example"})
		return v.Runtime.Set("handle", o)
	})
	if e != nil || string(out.JSON) != `{"id":"example"}` {
		t.Fatal(string(out.JSON), e)
	}
}
func TestTypedArrayOffsetRegression(t *testing.T) {
	out, e := Run(t.Context(), `function main(){const a=new Uint8Array([11,22,33,44]);return Array.from(a.subarray(1,3).filter(x=>true))}`, json.RawMessage(`null`), DefaultLimits(), nil)
	if e != nil || string(out.JSON) != `[22,33]` {
		t.Fatal(string(out.JSON), e)
	}
}
