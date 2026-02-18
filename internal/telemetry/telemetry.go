package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-logr/stdr"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("go.earthbuild.dev/earthbuild")

// Tracer returns the tracer for the EarthBuild CLI.
func Tracer() trace.Tracer {
	return tracer
}

// Setup bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func Setup(ctx context.Context) (shutdown func(context.Context) error, err error) {
	http.DefaultClient.Transport = otelhttp.NewTransport(http.DefaultTransport)

	otel.SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags)))

	var shutdownFuncs []func(context.Context) error

	// shutdown calls cleanup functions registered via shutdownFuncs.
	// The errors from the calls are joined.
	// Each registered cleanup will be invoked once.
	shutdown = func(ctx context.Context) error {
		var shutdownErr error

		for _, fn := range shutdownFuncs {
			shutdownErr = errors.Join(shutdownErr, fn(ctx))
		}

		shutdownFuncs = nil

		return shutdownErr
	}

	// handleError calls shutdown for cleanup and makes sure that all errors are returned.
	//nolint:unparam // shutdown is not used in the return value
	handleError := func(err error) (func(context.Context) error, error) {
		return nil, errors.Join(fmt.Errorf("setup telemetry: %w", err), shutdown(ctx))
	}

	// Set up propagator.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Set up tracer provider.
	var tracerProvider *sdktrace.TracerProvider

	tracerProvider, err = newTracerProvider(ctx)
	if err != nil {
		return handleError(err)
	}

	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
	otel.SetTracerProvider(tracerProvider)

	// Set up meter provider.
	var meterProvider *metric.MeterProvider

	meterProvider, err = newMeterProvider(ctx)
	if err != nil {
		return handleError(err)
	}

	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)

	// Set up logger provider.
	var loggerProvider *sdklog.LoggerProvider

	loggerProvider, err = newLoggerProvider()
	if err != nil {
		return handleError(err)
	}

	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	global.SetLoggerProvider(loggerProvider)

	return shutdown, nil
}

func newTracerProvider(ctx context.Context) (*sdktrace.TracerProvider, error) {
	errorf := func(format string, args ...any) (*sdktrace.TracerProvider, error) {
		return nil, fmt.Errorf("create tracer provider: "+format, args...)
	}

	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return errorf("create span exporter: %w", err)
	}

	executable, err := os.Executable()
	if err != nil {
		return errorf("get executable path: %w", err)
	}

	executablePath := filepath.Dir(executable)

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("EarthBuild"),
			semconv.ProcessCommand(os.Args[0]),
			semconv.ProcessPID(os.Getpid()),
			semconv.ProcessCommandArgs(os.Args[1:]...),
			semconv.ProcessExecutablePath(executablePath),
		),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return errorf("create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	return tp, nil
}

func newMeterProvider(ctx context.Context) (*metric.MeterProvider, error) {
	errorf := func(format string, args ...any) (*metric.MeterProvider, error) {
		return nil, fmt.Errorf("create meter provider: "+format, args...)
	}

	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return errorf("create metric reader: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return errorf("create resource: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(reader),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	return mp, nil
}

func newLoggerProvider() (*sdklog.LoggerProvider, error) {
	errorf := func(format string, args ...any) (*sdklog.LoggerProvider, error) {
		return nil, fmt.Errorf("create logger provider: "+format, args...)
	}

	logExporter, err := stdoutlog.New()
	if err != nil {
		return errorf("create log exporter: %w", err)
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
	)

	return loggerProvider, nil
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
