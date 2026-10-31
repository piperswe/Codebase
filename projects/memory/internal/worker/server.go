package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"codebase.bid/lib/go/jsruntime"
	"codebase.bid/projects/memory/internal/model"
	p "codebase.bid/projects/memory/proto/memory/worker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	p.UnimplementedWorkerServer
	busy     atomic.Bool
	Version  string
	HardStop func()
}

func (s *Server) Health(ctx context.Context, q *p.HealthRequest) (*p.HealthResponse, error) {
	if q.ProtocolVersion != model.RPCVersion {
		return nil, status.Error(codes.FailedPrecondition, "INCOMPATIBLE_PROTOCOL")
	}
	return &p.HealthResponse{ProtocolVersion: model.RPCVersion, WorkerVersion: s.Version, Ready: !s.busy.Load()}, nil
}
func (s *Server) Execute(stream grpc.BidiStreamingServer[p.ApiFrame, p.WorkerFrame]) error {
	if !s.busy.CompareAndSwap(false, true) {
		return status.Error(codes.ResourceExhausted, "WORKER_BUSY")
	}
	defer s.busy.Store(false)
	frame, e := stream.Recv()
	if e != nil {
		return e
	}
	start := frame.GetStart()
	if start == nil || !noUnknown(frame.ProtoReflect(), 0) || start.ProtocolVersion != model.RPCVersion || start.SdkVersion != model.SDKVersion || start.ExecutionId == "" || start.Limits == nil || start.Limits.TimeoutMs < 1 || start.Limits.TimeoutMs > 5000 || start.Limits.GraphOperations < 1 || start.Limits.GraphOperations > 100 || start.Limits.ResultBytes < 1 || start.Limits.ResultBytes > 262144 || start.Limits.LogBytes < 1 || start.Limits.LogBytes > 16384 {
		return status.Error(codes.InvalidArgument, "INCOMPATIBLE_PROTOCOL")
	}
	if e = stream.Send(&p.WorkerFrame{Frame: &p.WorkerFrame_Started{Started: &p.Started{ExecutionId: start.ExecutionId, ProtocolVersion: model.RPCVersion, WorkerVersion: s.Version}}}); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(stream.Context(), time.Duration(start.Limits.TimeoutMs)*time.Millisecond)
	defer cancel()
	// Hard-stop is installed only in the dedicated worker process. API deadlines
	// independently cancel streams and release snapshots if this process dies.
	var hard *time.Timer
	if s.HardStop != nil {
		hard = time.AfterFunc(time.Duration(start.Limits.TimeoutMs)*time.Millisecond+2*time.Second, s.HardStop)
		defer hard.Stop()
	}
	var calls uint64
	limits := jsruntime.DefaultLimits()
	limits.Timeout = time.Duration(start.Limits.TimeoutMs) * time.Millisecond
	limits.ResultBytes = int(start.Limits.ResultBytes)
	limits.LogBytes = int(start.Limits.LogBytes)
	out, e := jsruntime.Run(ctx, start.Source, start.InputJson, limits, func(v *jsruntime.VM) error {
		return Bind(v, func(q *p.GraphRequest) (*p.GraphResponse, error) {
			calls++
			if calls > uint64(start.Limits.GraphOperations) {
				return nil, model.Limit("Graph operation budget exhausted")
			}
			q.CallId = calls
			if e := stream.Send(&p.WorkerFrame{Frame: &p.WorkerFrame_Request{Request: q}}); e != nil {
				return nil, e
			}
			frame, e := stream.Recv()
			if e != nil {
				return nil, e
			}
			if frame.GetCancel() != nil {
				return nil, context.Canceled
			}
			res := frame.GetResponse()
			if res == nil || res.CallId != calls || !noUnknown(frame.ProtoReflect(), 0) {
				return nil, model.E("INCOMPATIBLE_PROTOCOL", "Unexpected graph response")
			}
			return res, nil
		})
	})
	stats := &p.Stats{GraphOperations: uint32(calls)}
	if e != nil {
		safe := &p.SafeError{Code: "SCRIPT_RUNTIME_ERROR", Message: "Execution failed"}
		var runtimeErr *jsruntime.Error
		if errors.As(e, &runtimeErr) {
			safe.Code = runtimeErr.Code
			safe.Message = runtimeErr.Message
		}
		return stream.Send(&p.WorkerFrame{Frame: &p.WorkerFrame_Failed{Failed: &p.Failed{Error: safe, Diagnostics: out.Diagnostics, Stats: stats}}})
	}
	return stream.Send(&p.WorkerFrame{Frame: &p.WorkerFrame_Completed{Completed: &p.Completed{ResultJson: out.JSON, Diagnostics: out.Diagnostics, Stats: stats}}})
}
