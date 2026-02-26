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
	var shutdowns []func(context.Context) error

	// shutdown calls cleanup functions registered via shutdowns.
	// The errors from the calls are joined.
	// Each registered cleanup will be invoked once.
	shutdown = func(ctx context.Context) error {
		var shutdownErr error

		for _, fn := range shutdowns {
			shutdownErr = errors.Join(shutdownErr, fn(ctx))
		}

		shutdowns = nil

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

	var tracerShutdown, meterShutdown, loggerShutdown func(context.Context) error

	tracerShutdown, err = setupTracerProvider(ctx)
	if err != nil {
		return handleError(err)
	}

	shutdowns = append(shutdowns, tracerShutdown)

	meterShutdown, err = setupMeterProvider(ctx)
	if err != nil {
		return handleError(err)
	}

	shutdowns = append(shutdowns, meterShutdown)

	loggerShutdown, err = setupLoggerProvider()
	if err != nil {
		return handleError(err)
	}

	shutdowns = append(shutdowns, loggerShutdown)

	return shutdown, nil
}

func setupTracerProvider(ctx context.Context) (shutdown func(context.Context) error, err error) {
	errorf := func(format string, args ...any) (func(context.Context) error, error) {
		return nil, fmt.Errorf("create tracer provider: "+format, args...)
	}

	err = optIn("OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	if err != nil {
		return errorf("%w", err)
	}

	http.DefaultClient.Transport = otelhttp.NewTransport(http.DefaultTransport)

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

	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

func setupMeterProvider(ctx context.Context) (func(context.Context) error, error) {
	errorf := func(format string, args ...any) (func(context.Context) error, error) {
		return nil, fmt.Errorf("create meter provider: "+format, args...)
	}

	err := optIn("OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	if err != nil {
		return errorf("%w", err)
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

	return mp.Shutdown, nil
}

func setupLoggerProvider() (func(context.Context) error, error) {
	errorf := func(format string, args ...any) (func(context.Context) error, error) {
		return nil, fmt.Errorf("create logger provider: "+format, args...)
	}

	err := optIn("OTEL_LOGS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")
	if err != nil {
		return errorf("%w", err)
	}

	otel.SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags)))

	logExporter, err := stdoutlog.New()
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
// The first environment variable MUST be OTEL_..._EXPORTER.
func optIn(key ...string) error {
	if !strings.HasPrefix(key[0], "OTEL_") || !strings.HasSuffix(key[0], "_EXPORTER") {
		return fmt.Errorf("first env var must be OTEL_..._EXPORTER: %s", key[0])
	}

	var found bool

	for _, k := range key {
		if _, ok := os.LookupEnv(k); ok {
			found = true
			break
		}
	}

	if found {
		return nil
	}

	err := os.Setenv(key[0], "none")
	if err != nil {
		return fmt.Errorf("set env var %s to none: %w", key[0], err)
	}

	return nil
}
