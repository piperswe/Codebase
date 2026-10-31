package worker

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"codebase.bid/projects/memory/internal/model"
	p "codebase.bid/projects/memory/proto/memory/worker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func protocolClient(t *testing.T) p.WorkerClient {
	t.Helper()
	l := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	p.RegisterWorkerServer(server, &Server{Version: "test"})
	go server.Serve(l)
	t.Cleanup(func() { server.Stop(); l.Close() })
	conn, e := grpc.NewClient("passthrough:///test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return l.Dial() }))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close() })
	return p.NewWorkerClient(conn)
}
func TestProtocolAndBusy(t *testing.T) {
	client := protocolClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, e := client.Health(ctx, &p.HealthRequest{ProtocolVersion: "old-version"}); e == nil {
		t.Fatal("old protocol accepted")
	}
	s, e := client.Execute(ctx)
	if e != nil {
		t.Fatal(e)
	}
	start := &p.StartExecution{ExecutionId: "test", ProtocolVersion: model.RPCVersion, SdkVersion: model.SDKVersion, Source: `function main(){return db.entity("device/test")}`, InputJson: json.RawMessage(`null`), Limits: &p.Limits{TimeoutMs: 500, GraphOperations: 2, ResultBytes: 1000, LogBytes: 1000}}
	if e = s.Send(&p.ApiFrame{Frame: &p.ApiFrame_Start{Start: start}}); e != nil {
		t.Fatal(e)
	}
	first, e := s.Recv()
	if e != nil || first.GetStarted() == nil {
		t.Fatal(first, e)
	}
	request, e := s.Recv()
	if e != nil || request.GetRequest() == nil || request.GetRequest().CallId != 1 {
		t.Fatal(request, e)
	}
	h, e := client.Health(ctx, &p.HealthRequest{ProtocolVersion: model.RPCVersion})
	if e != nil || h.Ready {
		t.Fatal("busy health", h, e)
	}
	// A second Start cannot be interpreted as callback data or restart execution.
	if e = s.Send(&p.ApiFrame{Frame: &p.ApiFrame_Start{Start: start}}); e != nil {
		t.Fatal(e)
	}
	last, e := s.Recv()
	if e != nil || last.GetFailed() == nil {
		t.Fatal("second start accepted", last, e)
	}
	s.CloseSend()
}
func TestResultValidation(t *testing.T) {
	for _, s := range []string{`{"type":"integer","value":"9223372036854775808"}`, `{"type":"integer","value":3}`, `not-json`} {
		if validateResult([]byte(s)) == nil {
			t.Fatal("unsafe result accepted", s)
		}
	}
}
