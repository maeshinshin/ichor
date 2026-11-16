package uiapi

import (
	"context"

	"connectrpc.com/connect"

	"github.com/maeshinshin/ichor/internal/version"
)

// VersionHeaderInterceptor sets X-Ichor-Version to the running Ichor version on an RPC response, before the
// handler runs, so an error carries it too. The SPA asks for a reload when its bundle was built from another
// version (ADR 0059, decision 7). Mount it after AuthInterceptor: a refused call never reaches it, so only an
// authenticated caller learns the version.
func VersionHeaderInterceptor() connect.Interceptor {
	return versionHeader{}
}

type versionHeader struct {
	serverOnly
}

func setVersionHeader(ctx context.Context) {
	if info, ok := connect.CallInfoForHandlerContext(ctx); ok {
		info.ResponseHeader().Set("X-Ichor-Version", version.Version)
	}
}

func (versionHeader) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		setVersionHeader(ctx)

		return next(ctx, req)
	}
}

func (versionHeader) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		setVersionHeader(ctx)

		return next(ctx, conn)
	}
}
