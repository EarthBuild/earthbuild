package observe

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
)

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
		fmt.Println("Shutdown OTel providers")

		var inErr error

		for _, fn := range shutdownFuncs {
			inErr = errors.Join(inErr, fn(ctx))
		}

		shutdownFuncs = nil

		return inErr
	}

	// handleErr calls shutdown for cleanup and makes sure that all errors are returned.
	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx))
	}

	// Set up propagator.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	tracerProvider, err := newTracerProvider(ctx)
	if err != nil {
		return nil, err
	}

	shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
	otel.SetTracerProvider(tracerProvider)

	// Set up meter provider.
	meterProvider, err := newMeterProvider(ctx)
	if err != nil {
		handleErr(err)
		return shutdown, err
	}

	shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
	otel.SetMeterProvider(meterProvider)

	// Set up logger provider.
	loggerProvider, err := newLoggerProvider()
	if err != nil {
		handleErr(err)
		return shutdown, err
	}

	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	global.SetLoggerProvider(loggerProvider)

	return shutdown, err
}

func newTracerProvider(ctx context.Context) (*sdktrace.TracerProvider, error) {
	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}

	executable, err := os.Executable()
	if err != nil {
		panic(err)
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
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	return tp, nil
}

func newMeterProvider(ctx context.Context) (*metric.MeterProvider, error) {
	// 1. Automatically create a MetricReader based on OTEL_METRICS_EXPORTER
	// This handles OTLP, Prometheus, or Console automatically.
	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Define the Resource (Service Name, etc.)
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, err
	}

	// 3. Create the MeterProvider
	mp := metric.NewMeterProvider(
		metric.WithReader(reader),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	return mp, nil
}

func newLoggerProvider() (*sdklog.LoggerProvider, error) {
	logExporter, err := stdoutlog.New()
	if err != nil {
		return nil, err
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
	)

	return loggerProvider, nil
}
