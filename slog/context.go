package slog

import "context"

type contextKey string

const loggerContextKey = contextKey("logger")

// GetLogger returns a logger associated with this context.
func GetLogger(ctx context.Context) Logger {
	v := ctx.Value(loggerContextKey)
	if v == nil {
		return Logger{}
	}

	logger, _ := v.(Logger)

	return logger
}
