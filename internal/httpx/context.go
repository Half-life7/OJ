package httpx

import "context"

type styleNonceKey struct{}

func WithStyleNonce(ctx context.Context, nonce string) context.Context {
	return context.WithValue(ctx, styleNonceKey{}, nonce)
}

// StyleNonce authorizes only styles created by the local code editor.
func StyleNonce(ctx context.Context) string {
	nonce, _ := ctx.Value(styleNonceKey{}).(string)
	return nonce
}
