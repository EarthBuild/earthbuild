package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-logr/stdr"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("go.earthbuild.dev/earthbuild")

// Tracer returns the tracer for the EarthBuild CLI.
func Tracer() trace.Tracer {
	return tracer
}

// Setup bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func Setup(ctx context.Context) (ShutdownFunc, error) {
	otel.SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags)))

	var shutdowns []ShutdownFunc

	// shutdown calls cleanup functions registered via shutdowns.
	// The errors from the calls are joined.
	// Each registered cleanup will be invoked once.
	shutdown := func(ctx context.Context) error {
		var shutdownErr error

		for _, fn := range shutdowns {
			shutdownErr = errors.Join(shutdownErr, fn(ctx))
		}

		shutdowns = nil

		return shutdownErr
	}

	// handleError calls shutdown for cleanup and makes sure that all errors are returned.
	handleError := func(err error) (ShutdownFunc, error) {
		return nil, errors.Join(fmt.Errorf("setup telemetry: %w", err), shutdown(ctx))
	}

	// Set up propagator.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	var tracerShutdown, meterShutdown, loggerShutdown ShutdownFunc

	otelResource, err := newOTelResource(ctx)
	if err != nil {
		return handleError(err)
	}

	tracerShutdown, err = setupTracerProvider(ctx, otelResource)
	if err != nil {
		return handleError(err)
	}

	shutdowns = append(shutdowns, tracerShutdown)

	meterShutdown, err = setupMeterProvider(ctx, otelResource)
	if err != nil {
		return handleError(err)
	}

	shutdowns = append(shutdowns, meterShutdown)

	loggerShutdown, err = setupLoggerProvider(ctx)
	if err != nil {
		return handleError(err)
	}

	shutdowns = append(shutdowns, loggerShutdown)

	return shutdown, nil
}

// ShutdownFunc shuts down all OTel providers.
type ShutdownFunc func(context.Context) error

func newOTelResource(ctx context.Context) (*resource.Resource, error) {
	errorf := func(format string, args ...any) (*resource.Resource, error) {
		return nil, fmt.Errorf("create OTel resource: "+format, args...)
	}

	executable, err := os.Executable()
	if err != nil {
		return errorf("get executable path: %w", err)
	}

	var otelResource *resource.Resource

	otelResource, err = resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("EarthBuild"),
			semconv.ProcessCommand(filepath.Base(executable)),
			semconv.ProcessPID(os.Getpid()),
			semconv.ProcessCommandArgs(os.Args...),
			semconv.ProcessExecutablePath(executable),
		),
	)
	if err != nil {
		return errorf("%w", err)
	}

	otelResource, err = resource.Merge(resource.Default(), otelResource)
	if err != nil {
		return errorf("%w", err)
	}

	return otelResource, nil
}

func setupTracerProvider(ctx context.Context, res *resource.Resource) (ShutdownFunc, error) {
	errorf := func(format string, args ...any) (ShutdownFunc, error) {
		return nil, fmt.Errorf("create tracer provider: "+format, args...)
	}

	enabled, err := optIn("OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	if err != nil {
		return errorf("%w", err)
	}

	if enabled {
		http.DefaultClient.Transport = otelhttp.NewTransport(http.DefaultTransport)
	}

	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return errorf("create span exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

func setupMeterProvider(ctx context.Context, res *resource.Resource) (ShutdownFunc, error) {
	errorf := func(format string, args ...any) (ShutdownFunc, error) {
		return nil, fmt.Errorf("create meter provider: "+format, args...)
	}

	_, err := optIn("OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	if err != nil {
		return errorf("%w", err)
	}

	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return errorf("create metric reader: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(reader),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	return mp.Shutdown, nil
}

func setupLoggerProvider(ctx context.Context) (ShutdownFunc, error) {
	errorf := func(format string, args ...any) (ShutdownFunc, error) {
		return nil, fmt.Errorf("create logger provider: "+format, args...)
	}

	_, err := optIn("OTEL_LOGS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")
	if err != nil {
		return errorf("%w", err)
	}

	logExporter, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return errorf("create log exporter: %w", err)
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
	)

	global.SetLoggerProvider(loggerProvider)

	return loggerProvider.Shutdown, nil
}

// WithTraceparent returns a context with the traceparent (W3C Trace Context format)
// extracted from the environment variable TRACEPARENT.
func WithTraceparent(ctx context.Context) context.Context {
	traceparent := os.Getenv("TRACEPARENT")
	if traceparent == "" {
		return ctx
	}

	carrier := propagation.MapCarrier{
		"traceparent": traceparent,
	}

	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}

// optIn checks if user has opted into telemetry by setting any of the environment variables.
// Returns true if the user has opted in, false otherwise.
// The first environment variable MUST be OTEL_..._EXPORTER.
func optIn(key ...string) (bool, error) {
	if !strings.HasPrefix(key[0], "OTEL_") || !strings.HasSuffix(key[0], "_EXPORTER") {
		return false, fmt.Errorf("first env var must be OTEL_..._EXPORTER: %s", key[0])
	}

	for _, k := range key {
		if _, ok := os.LookupEnv(k); ok {
			return true, nil
		}
	}

	err := os.Setenv(key[0], "none")
	if err != nil {
		return false, fmt.Errorf("set env var %s to none: %w", key[0], err)
	}

	return false, nil
}
