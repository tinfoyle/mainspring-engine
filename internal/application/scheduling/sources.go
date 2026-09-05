package scheduling

import "context"

type sourceContextKey struct{}

func WithSourceContext(ctx context.Context, text string) context.Context {
	return context.WithValue(ctx, sourceContextKey{}, text)
}
func SourceContext(ctx context.Context) string {
	value, _ := ctx.Value(sourceContextKey{}).(string)
	return value
}
