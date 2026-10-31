package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"codebase.bid/lib/go/unixsocket"
	"codebase.bid/projects/memory/internal/model"
	"codebase.bid/projects/memory/internal/store/postgres"
	p "codebase.bid/projects/memory/proto/memory/worker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

type Client struct {
	conn                                              *grpc.ClientConn
	rpc                                               p.WorkerClient
	busy                                              atomic.Bool
	TimeoutMS, GraphOperations, ResultBytes, LogBytes int
}

func Dial(path string, peerUID int) (*Client, error) {
	if !filepath.IsAbs(path) {
		return nil, model.Invalid("worker Unix socket")
	}
	conn, e := grpc.NewClient("passthrough:///worker", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return unixsocket.Dial(ctx, path, peerUID) }), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1<<20), grpc.MaxCallSendMsgSize(1<<20)))
	if e != nil {
		return nil, e
	}
	return &Client{conn: conn, rpc: p.NewWorkerClient(conn), TimeoutMS: 5000, GraphOperations: 100, ResultBytes: 262144, LogBytes: 16384}, nil
}
func (c *Client) Close() error { return c.conn.Close() }
func (c *Client) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	h, e := c.rpc.Health(ctx, &p.HealthRequest{ProtocolVersion: model.RPCVersion})
	if e != nil {
		return &model.Error{Code: "WORKER_UNAVAILABLE", Message: "Code Mode worker is unavailable", Retryable: true}
	}
	if h.ProtocolVersion != model.RPCVersion {
		return model.E("INCOMPATIBLE_PROTOCOL", "Worker protocol mismatch")
	}
	if !h.Ready {
		return &model.Error{Code: "WORKER_BUSY", Message: "Worker is busy; retry later", Retryable: true}
	}
	return nil
}

type Result struct {
	Value       json.RawMessage `json:"value"`
	Diagnostics string          `json:"diagnostics"`
	Stats       *p.Stats        `json:"stats"`
}

func (c *Client) Execute(ctx context.Context, store *postgres.Store, principal model.Principal, q model.Code) (Result, model.Meta, error) {
	var out Result
	var meta model.Meta
	if !principal.Allow(q.Space, "memory:execute") || !principal.Allow(q.Space, "memory:read") {
		return out, meta, model.E("PERMISSION_DENIED", "Code Mode requires read and execute grants for this space")
	}
	if !c.busy.CompareAndSwap(false, true) {
		return out, meta, &model.Error{Code: "WORKER_BUSY", Message: "Worker is busy; retry later", Retryable: true}
	}
	defer c.busy.Store(false)
	if len(q.Source) > 65536 || len(q.Input) > 65536 || !json.Valid(q.Input) || q.TimeoutMS < 0 || q.TimeoutMS > c.TimeoutMS {
		return out, meta, model.Limit("Script, input or timeout exceeds limit")
	}
	if q.TimeoutMS == 0 {
		q.TimeoutMS = c.TimeoutMS
	}
	if e := c.Ready(ctx); e != nil {
		return out, meta, e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(q.TimeoutMS)*time.Millisecond)
	defer cancel()
	r, e := store.Read(ctx, principal, q.Space)
	if e != nil {
		return out, meta, e
	}
	defer r.Close()
	meta = model.Meta{RequestID: model.NewID(), SpaceRevision: model.Revision(r.Revision), SchemaRevision: model.Revision(r.SchemaRevision), Complete: true}
	stream, e := c.rpc.Execute(ctx)
	if e != nil {
		return out, meta, unavailable(ctx)
	}
	defer stream.CloseSend()
	id := model.NewID()
	start := &p.StartExecution{ExecutionId: id, ProtocolVersion: model.RPCVersion, SdkVersion: model.SDKVersion, Source: q.Source, InputJson: q.Input, SchemaRevision: meta.SchemaRevision, SpaceRevision: meta.SpaceRevision, Limits: &p.Limits{TimeoutMs: uint32(q.TimeoutMS), GraphOperations: uint32(c.GraphOperations), ResultBytes: uint32(c.ResultBytes), LogBytes: uint32(c.LogBytes)}}
	if e = stream.Send(&p.ApiFrame{Frame: &p.ApiFrame_Start{Start: start}}); e != nil {
		return out, meta, unavailable(ctx)
	}
	frame, e := stream.Recv()
	if e != nil {
		return out, meta, unavailable(ctx)
	}
	started := frame.GetStarted()
	if started == nil || started.ExecutionId != id || started.ProtocolVersion != model.RPCVersion || !noUnknown(frame.ProtoReflect(), 0) {
		return out, meta, model.E("INCOMPATIBLE_PROTOCOL", "Invalid worker handshake")
	}
	var last uint64
	for {
		frame, e = stream.Recv()
		if e != nil {
			return out, meta, unavailable(ctx)
		}
		if !noUnknown(frame.ProtoReflect(), 0) {
			return out, meta, model.E("INCOMPATIBLE_PROTOCOL", "Unknown worker frame")
		}
		switch x := frame.Frame.(type) {
		case *p.WorkerFrame_Request:
			if x.Request.CallId != last+1 || last >= uint64(c.GraphOperations) {
				return out, meta, model.E("INCOMPATIBLE_PROTOCOL", "Invalid callback sequence")
			}
			last++
			res := Dispatch(r, x.Request)
			if err := res.GetError(); err != nil && (err.Code == "QUERY_LIMIT_EXCEEDED" || err.Code == "RESOURCE_LIMIT_EXCEEDED" || err.Code == "PERMISSION_DENIED" || err.Code == "INCOMPATIBLE_PROTOCOL") {
				return out, meta, &model.Error{Code: err.Code, Message: err.Message, Retryable: err.Retryable}
			}
			if e = stream.Send(&p.ApiFrame{Frame: &p.ApiFrame_Response{Response: res}}); e != nil {
				return out, meta, unavailable(ctx)
			}
		case *p.WorkerFrame_Completed:
			if len(x.Completed.ResultJson) > c.ResultBytes || len(x.Completed.Diagnostics) > c.LogBytes || !json.Valid(x.Completed.ResultJson) || proto.Size(frame) > 1<<20 {
				return out, meta, model.E("UNSUPPORTED_RESULT", "Worker returned an invalid or oversized result")
			}
			// Decode with a bound before accepting worker-selected JSON. All graph data
			// it can obtain has already passed the API's execution-owned read session.
			if e = validateResult(x.Completed.ResultJson); e != nil {
				return out, meta, e
			}
			out = Result{Value: x.Completed.ResultJson, Diagnostics: x.Completed.Diagnostics, Stats: &p.Stats{GraphOperations: uint32(last), SqlStatements: uint32(r.Statements), Rows: uint32(r.Rows), Edges: uint32(r.Edges)}}
			return out, meta, nil
		case *p.WorkerFrame_Failed:
			err := x.Failed.Error
			if err == nil || len(err.Message) > 256 || len(err.Code) > 80 {
				return out, meta, model.E("INCOMPATIBLE_PROTOCOL", "Invalid worker failure")
			}
			return out, meta, &model.Error{Code: err.Code, Message: "Code Mode execution failed", Retryable: err.Retryable}
		default:
			return out, meta, model.E("INCOMPATIBLE_PROTOCOL", "Illegal worker state transition")
		}
	}
}
func unavailable(ctx context.Context) error {
	if ctx.Err() != nil {
		return &model.Error{Code: "EXECUTION_TIMEOUT", Message: "Execution timed out or was cancelled", Retryable: true}
	}
	return &model.Error{Code: "WORKER_UNAVAILABLE", Message: "Worker disconnected; retry explicitly", Retryable: true}
}
func validateResult(b []byte) error {
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		return model.E("UNSUPPORTED_RESULT", "Invalid result JSON")
	}
	nodes := 0
	var walk func(any, int) error
	walk = func(x any, depth int) error {
		nodes++
		if depth > 32 || nodes > 10000 {
			return model.E("UNSUPPORTED_RESULT", "Result exceeds nesting/node budget")
		}
		switch x := x.(type) {
		case json.Number:
			n, e := strconv.ParseFloat(string(x), 64)
			if e != nil || math.IsInf(n, 0) || math.IsNaN(n) {
				return model.E("UNSUPPORTED_RESULT", "Non-finite worker number")
			}

		case []any:
			for _, v := range x {
				if e := walk(v, depth+1); e != nil {
					return e
				}
			}
		case map[string]any:
			if typ, ok := x["type"].(string); ok && typ == "integer" {
				s, ok := x["value"].(string)
				if !ok {
					return model.E("UNSUPPORTED_RESULT", "Invalid tagged integer")
				}
				if _, e := (model.Value{Type: model.Integer, Text: s}).Canonical(); e != nil {
					return e
				}
			}
			for _, v := range x {
				if e := walk(v, depth+1); e != nil {
					return e
				}
			}
		}
		return nil
	}
	return walk(v, 0)
}
