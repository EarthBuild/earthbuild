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

const (
	cliServiceName       = "EarthBuild"
	buildkitdServiceName = "EarthBuild-buildkitd"

	sdkDisabledEnv     = "OTEL_SDK_DISABLED"
	otlpEndpointEnv    = "OTEL_EXPORTER_OTLP_ENDPOINT"
	tracesExporterEnv  = "OTEL_TRACES_EXPORTER"
	metricsExporterEnv = "OTEL_METRICS_EXPORTER"
	logsExporterEnv    = "OTEL_LOGS_EXPORTER"
	tracesEndpointEnv  = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	metricsEndpointEnv = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	logsEndpointEnv    = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
	resourceAttrEnv    = "OTEL_RESOURCE_ATTRIBUTES"
	serviceNameEnv     = "OTEL_SERVICE_NAME"
)

var tracer = otel.Tracer("go.earthbuild.dev/earthbuild")

// Tracer returns the tracer for the earth CLI.
func Tracer() trace.Tracer {
	return tracer
}

// Setup bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func Setup(ctx context.Context) (ShutdownFunc, error) {
	tracesEnabled, metricsEnabled, logsEnabled := signalsOptIn(os.Getenv)
	if !tracesEnabled && !metricsEnabled && !logsEnabled {
		return noopShutdown, nil
	}

	otelResource, err := newOTelResource(ctx)
	if err != nil {
		return nil, fmt.Errorf("setup telemetry: %w", err)
	}

	prevPropagator := otel.GetTextMapPropagator()

	// Set up propagator.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	shutdowns := make([]ShutdownFunc, 0, 4)
	shutdowns = append(shutdowns, func(context.Context) error {
		otel.SetTextMapPropagator(prevPropagator)

		return nil
	})

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

	if tracesEnabled {
		tracerShutdown, err := setupTracerProvider(ctx, otelResource)
		if err != nil {
			return handleError(err)
		}

		shutdowns = append(shutdowns, tracerShutdown)
	}

	if metricsEnabled {
		meterShutdown, err := setupMeterProvider(ctx, otelResource)
		if err != nil {
			return handleError(err)
		}

		shutdowns = append(shutdowns, meterShutdown)
	}

	if logsEnabled {
		loggerShutdown, err := setupLoggerProvider(ctx, otelResource)
		if err != nil {
			return handleError(err)
		}

		shutdowns = append(shutdowns, loggerShutdown)
	}

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
			otelsemconv.ServiceName(cliServiceName),
			otelsemconv.ProcessCommand(filepath.Base(executable)),
			otelsemconv.ProcessPID(os.Getpid()),
			otelsemconv.ProcessCommandArgs(os.Args...),
			otelsemconv.ProcessExecutablePath(executable),
			semconv.ProcessRoleCLI,
			processNesting(),
		),
	)
	if errors.Is(err, resource.ErrPartialResource) || errors.Is(err, resource.ErrSchemaURLConflict) {
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
	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("create tracer provider: create span exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	prevTransport := http.DefaultClient.Transport
	otelTransport := otelhttp.NewTransport(prevTransport)
	http.DefaultClient.Transport = otelTransport

	prevTP := otel.GetTracerProvider()

	otel.SetTracerProvider(tp)

	return func(ctx context.Context) error {
		otel.SetTracerProvider(prevTP)

		if http.DefaultClient.Transport == otelTransport {
			http.DefaultClient.Transport = prevTransport
		}

		return tp.Shutdown(ctx)
	}, nil
}

func setupMeterProvider(ctx context.Context, res *resource.Resource) (ShutdownFunc, error) {
	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("create meter provider: create metric reader: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(reader),
		metric.WithResource(res),
	)

	err = otelruntime.Start(otelruntime.WithMeterProvider(mp))
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("create meter provider: initialize runtime metrics: %w", err),
			mp.Shutdown(ctx),
		)
	}

	prevMP := otel.GetMeterProvider()

	otel.SetMeterProvider(mp)

	return func(ctx context.Context) error {
		otel.SetMeterProvider(prevMP)

		return mp.Shutdown(ctx)
	}, nil
}

func processNesting() attribute.KeyValue {
	// A typo only mis-labels the datapoint as outer, so it is reported and shrugged
	// off rather than failing telemetry setup over it.
	withDocker, err := env.Bool("WITH_DOCKER")
	if err != nil {
		otel.Handle(err)
	}

	return processNestingAttr(withDocker)
}

func processNestingAttr(withDocker bool) attribute.KeyValue {
	if withDocker {
		return semconv.ProcessNestingInner
	}

	return semconv.ProcessNestingOuter
}

func setupLoggerProvider(ctx context.Context, res *resource.Resource) (ShutdownFunc, error) {
	logExporter, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("create logger provider: create log exporter: %w", err)
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)

	prevLP := global.GetLoggerProvider()

	global.SetLoggerProvider(loggerProvider)

	return func(ctx context.Context) error {
		global.SetLoggerProvider(prevLP)

		return loggerProvider.Shutdown(ctx)
	}, nil
}

// WithTraceparent returns a context with the traceparent (W3C Trace Context format)
// extracted from the environment variable TRACEPARENT.
func WithTraceparent(ctx context.Context) context.Context {
	traceparent := os.Getenv("TRACEPARENT")
	if traceparent == "" {
		return ctx
	}

	return (propagation.TraceContext{}).Extract(ctx, propagation.MapCarrier{
		"traceparent": traceparent,
	})
}

// enabled reports whether any OpenTelemetry signal (traces, metrics, or logs)
// is opted in according to the OTEL_* variables in env.
func enabled(env map[string]string) bool {
	traces, metrics, logs := signalsOptIn(func(key string) string { return env[key] })

	return traces || metrics || logs
}

func signalsOptIn(lookup func(string) string) (traces, metrics, logs bool) {
	if isSDKDisabled(lookup) {
		return false, false, false
	}

	otlpEndpoint := strings.TrimSpace(lookup(otlpEndpointEnv))

	return signalOptIn(lookup, otlpEndpoint, tracesExporterEnv, tracesEndpointEnv),
		signalOptIn(lookup, otlpEndpoint, metricsExporterEnv, metricsEndpointEnv),
		signalOptIn(lookup, otlpEndpoint, logsExporterEnv, logsEndpointEnv)
}

func isSDKDisabled(lookup func(string) string) bool {
	disabled, err := strconv.ParseBool(strings.TrimSpace(lookup(sdkDisabledEnv)))

	return err == nil && disabled
}

func signalOptIn(lookup func(string) string, otlpEndpoint, exporterKey, signalEndpointKey string) bool {
	exporter := strings.TrimSpace(lookup(exporterKey))
	if strings.EqualFold(exporter, "none") {
		return false
	}

	return exporter != "" || otlpEndpoint != "" || strings.TrimSpace(lookup(signalEndpointKey)) != ""
}
