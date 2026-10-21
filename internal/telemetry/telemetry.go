// Package telemetry implements OpenTelemetry tracing and metrics collection for EarthBuild's internal operations.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/EarthBuild/earthbuild/internal/env"
	"github.com/EarthBuild/earthbuild/internal/telemetry/semconv"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	otelsemconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("go.earthbuild.dev/earthbuild")

// Tracer returns the tracer for the earth CLI.
func Tracer() trace.Tracer {
	return tracer
}

// Setup bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func Setup(ctx context.Context) (ShutdownFunc, error) {
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

	loggerShutdown, err = setupLoggerProvider(ctx, otelResource)
	if err != nil {
		return handleError(err)
	}

	shutdowns = append(shutdowns, loggerShutdown)

	return shutdown, nil
}

// ShutdownFunc shuts down all OTel providers.
type ShutdownFunc func(context.Context) error

func newOTelResource(ctx context.Context) (*resource.Resource, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("create OTel resource: get executable path: %w", err)
	}

	// The CI and VCS attributes CI sets in OTEL_RESOURCE_ATTRIBUTES are read by
	// WithFromEnv; WithAttributes comes after so earth's own identity wins on collision.
	otelResource, err := resource.New(
		ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			otelsemconv.ServiceName("EarthBuild"),
			otelsemconv.ProcessCommand(filepath.Base(executable)),
			otelsemconv.ProcessPID(os.Getpid()),
			otelsemconv.ProcessCommandArgs(os.Args...),
			otelsemconv.ProcessExecutablePath(executable),
			semconv.ProcessRoleCLI,
			earthbuildProcessNesting(),
		),
	)
	if errors.Is(err, resource.ErrPartialResource) {
		otel.Handle(err)

		return otelResource, nil
	}

	if err != nil {
		return nil, fmt.Errorf("create OTel resource: %w", err)
	}

	return otelResource, nil
}

func noopShutdown(context.Context) error { return nil }

func setupTracerProvider(ctx context.Context, res *resource.Resource) (ShutdownFunc, error) {
	if !optIn("OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") {
		return noopShutdown, nil
	}

	http.DefaultClient.Transport = otelhttp.NewTransport(http.DefaultTransport)

	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("create tracer provider: create span exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

func setupMeterProvider(ctx context.Context, res *resource.Resource) (ShutdownFunc, error) {
	if !optIn("OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") {
		return noopShutdown, nil
	}

	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("create meter provider: create metric reader: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(reader),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	// Hand otelruntime the provider we just built rather than let it resolve the global
	// one: it is the same object, but only because SetMeterProvider happens to run first,
	// and nothing here enforces that ordering.
	err = otelruntime.Start(otelruntime.WithMeterProvider(mp))
	if err != nil {
		return nil, fmt.Errorf("create meter provider: initialize runtime metrics: %w", err)
	}

	return mp.Shutdown, nil
}

func earthbuildProcessNesting() attribute.KeyValue {
	// A typo only mis-labels the datapoint as outer, so it is reported and shrugged
	// off rather than failing telemetry setup over it.
	withDocker, err := env.Bool("WITH_DOCKER")
	if err != nil {
		otel.Handle(err)
	}

	if withDocker {
		return semconv.ProcessNestingInner
	}

	return semconv.ProcessNestingOuter
}

func setupLoggerProvider(ctx context.Context, res *resource.Resource) (ShutdownFunc, error) {
	if !optIn("OTEL_LOGS_EXPORTER", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT") {
		return noopShutdown, nil
	}

	logExporter, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("create logger provider: create log exporter: %w", err)
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
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

// optIn reports whether the user has opted into a telemetry signal.
// A signal is enabled when OTEL_SDK_DISABLED is not true, exporterKey is not
// "none", and at least one of exporterKey, OTEL_EXPORTER_OTLP_ENDPOINT, or
// signalEndpointKey is non-empty.
func optIn(exporterKey, signalEndpointKey string) bool {
	disabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")))
	if err == nil && disabled {
		return false
	}

	exporter := strings.TrimSpace(os.Getenv(exporterKey))
	if strings.EqualFold(exporter, "none") {
		return false
	}

	return exporter != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv(signalEndpointKey)) != ""
}
