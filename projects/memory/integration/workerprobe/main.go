// workerprobe is a test-only protocol client. Destructive modes are run only
// against the disposable cgroup-limited image test, never an owner's service.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"codebase.bid/lib/go/unixsocket"
	"codebase.bid/projects/memory/internal/model"
	p "codebase.bid/projects/memory/proto/memory/worker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("workerprobe SOCKET good|timeout|oom")
	}
	mode := os.Args[2]
	source := `function main(){return {ok:true,integer:9223372036854775807n}}`
	timeout := uint32(5000)
	switch mode {
	case "good":
	case "timeout":
		source = `function main(){while(true){}}`
		timeout = 100
	case "oom":
		source = `function main(){const arrays=[];while(true){arrays.push(new Array(100000).fill("synthetic-allocation"))}}`
	default:
		return fmt.Errorf("unknown test mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, e := grpc.NewClient("passthrough:///test-worker", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return unixsocket.Dial(ctx, os.Args[1], os.Getuid())
	}), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1<<20)))
	if e != nil {
		return e
	}
	defer c.Close()
	client := p.NewWorkerClient(c)
	stream, e := client.Execute(ctx)
	if e != nil {
		return e
	}
	defer stream.CloseSend()
	e = stream.Send(&p.ApiFrame{Frame: &p.ApiFrame_Start{Start: &p.StartExecution{ExecutionId: "disposable-image-probe", ProtocolVersion: model.RPCVersion, SdkVersion: model.SDKVersion, Source: source, InputJson: []byte(`{}`), Limits: &p.Limits{TimeoutMs: timeout, GraphOperations: 1, ResultBytes: 1024, LogBytes: 1024}}}})
	if e != nil {
		return e
	}
	first, e := stream.Recv()
	if e != nil || first.GetStarted() == nil {
		return fmt.Errorf("probe handshake failed: %v", e)
	}
	result, e := stream.Recv()
	if mode == "oom" {
		if status.Code(e) == codes.Unavailable || status.Code(e) == codes.Internal {
			return nil
		}
		return fmt.Errorf("expected worker process loss, got %v %v", result, e)
	}
	if e != nil {
		return e
	}
	if mode == "timeout" {
		if result.GetFailed() == nil || result.GetFailed().Error.Code != "EXECUTION_TIMEOUT" {
			return fmt.Errorf("expected cooperative timeout")
		}
		return nil
	}
	complete := result.GetCompleted()
	if complete == nil || !json.Valid(complete.ResultJson) {
		return fmt.Errorf("worker did not complete safely")
	}
	return nil
}
