package model

import (
	"context"
	"time"
)

type RequestInfo struct {
	ID         string
	ReceivedAt time.Time
}
type requestKey struct{}

// EnsureRequest binds server-derived audit identity once at a transport/operator
// boundary. Neither ordinary request JSON nor worker frames can supply it.
func EnsureRequest(ctx context.Context) (context.Context, RequestInfo) {
	if info, ok := ctx.Value(requestKey{}).(RequestInfo); ok {
		return ctx, info
	}
	info := RequestInfo{ID: NewID(), ReceivedAt: time.Now().UTC()}
	return context.WithValue(ctx, requestKey{}, info), info
}
