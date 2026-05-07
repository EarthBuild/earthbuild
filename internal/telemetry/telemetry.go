package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"

	"github.com/go-logr/stdr"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log/global"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
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

		if shutdownErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: OpenTelemetry shutdown failed; continuing: %s\n", shutdownErr)
		}

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

	err = otelruntime.Start()
	if err != nil {
		return errorf("initialize runtime metrics: %w", err)
	}

	err = setupProcessMemoryMetrics()
	if err != nil {
		return errorf("initialize process memory metrics: %w", err)
	}

	return mp.Shutdown, nil
}

func setupProcessMemoryMetrics() error {
	meter := otel.Meter("go.earthbuild.dev/earthbuild/process")
	attrs := processMemoryMetricAttributes()

	err := registerProcessMemoryGauge(
		meter,
		attrs,
		"earthbuild_process_memory_alloc_bytes",
		"Bytes allocated and still in use by this EarthBuild process.",
		func(stats goruntime.MemStats) uint64 { return stats.Alloc },
	)
	if err != nil {
		return err
	}

	err = registerProcessMemoryGauge(
		meter,
		attrs,
		"earthbuild_process_memory_heap_alloc_bytes",
		"Heap bytes allocated and still in use by this EarthBuild process.",
		func(stats goruntime.MemStats) uint64 { return stats.HeapAlloc },
	)
	if err != nil {
		return err
	}

	err = registerProcessMemoryGauge(
		meter,
		attrs,
		"earthbuild_process_memory_heap_sys_bytes",
		"Heap bytes obtained from the OS by this EarthBuild process.",
		func(stats goruntime.MemStats) uint64 { return stats.HeapSys },
	)
	if err != nil {
		return err
	}

	return registerProcessMemoryGauge(
		meter,
		attrs,
		"earthbuild_process_memory_sys_bytes",
		"Total bytes obtained from the OS by this EarthBuild process.",
		func(stats goruntime.MemStats) uint64 { return stats.Sys },
	)
}

func registerProcessMemoryGauge(
	meter otelmetric.Meter,
	attrs []attribute.KeyValue,
	name string,
	description string,
	value func(goruntime.MemStats) uint64,
) error {
	_, err := meter.Int64ObservableGauge(
		name,
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription(description),
		otelmetric.WithInt64Callback(func(_ context.Context, observer otelmetric.Int64Observer) error {
			var stats goruntime.MemStats
			goruntime.ReadMemStats(&stats)

			observer.Observe(clampUint64ToInt64(value(stats)), otelmetric.WithAttributes(attrs...))

			return nil
		}),
	)
	if err != nil {
		return fmt.Errorf("create %s gauge: %w", name, err)
	}

	return nil
}

func clampUint64ToInt64(value uint64) int64 {
	const maxInt64 = uint64(1<<63 - 1)

	if value > maxInt64 {
		return int64(maxInt64)
	}

	return int64(value)
}

func processMemoryMetricAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.Int("process.pid", os.Getpid()),
		attribute.String("earthbuild.process.role", "earthbuild-cli"),
		attribute.String("earthbuild.process.nesting", earthbuildProcessNesting()),
	}

	for _, key := range []string{
		"cicd.pipeline.name",
		"cicd.pipeline.run.id",
		"cicd.pipeline.run.url.full",
		"cicd.system.name",
		"deployment.environment",
		"user.id",
		"vcs.ref.name",
		"vcs.repository.change.id",
		"vcs.repository.name",
		"vcs.revision.id",
	} {
		if value, ok := otelResourceAttributeFromEnv(key); ok {
			attrs = append(attrs, attribute.String(key, value))
		}
	}

	if target := earthbuildTargetFromArgs(os.Args); target != "" {
		attrs = append(attrs, attribute.String("earthbuild.target", target))
	}

	return attrs
}

func earthbuildProcessNesting() string {
	if value, _ := strconv.ParseBool(os.Getenv("EARTHLY_WITH_DOCKER")); value {
		return "inner"
	}

	return "outer"
}

func otelResourceAttributeFromEnv(key string) (string, bool) {
	for attr := range strings.SplitSeq(os.Getenv("OTEL_RESOURCE_ATTRIBUTES"), ",") {
		attrKey, value, ok := strings.Cut(attr, "=")
		if !ok || strings.TrimSpace(attrKey) != key {
			continue
		}

		value = strings.TrimSpace(value)

		return value, value != ""
	}

	return "", false
}

func earthbuildTargetFromArgs(args []string) string {
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}

		if strings.Contains(arg, "+") {
			return arg
		}
	}

	return ""
}

func setupLoggerProvider(ctx context.Context, res *resource.Resource) (ShutdownFunc, error) {
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
