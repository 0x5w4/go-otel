package tracer

import (
	"context"
	"fmt"
	"net/url"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdkTrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.9.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/credentials"
)

var _ Tracer = (*otelTracer)(nil)

type Tracer interface {
	Tracer() trace.Tracer
	TracerProvider() trace.TracerProvider
	Shutdown(ctx context.Context) error
}

type otelTracer struct {
	tracer         trace.Tracer
	tracerProvider trace.TracerProvider
}

type Config struct {
	ExporterURL           string
	SecretToken           string
	ServiceName           string
	ServiceVersion        string
	DeploymentEnvironment string
	Creds                 *credentials.TransportCredentials
	Sampler               *sdkTrace.Sampler
}

func InitTracer(ctx context.Context, cfg *Config) (*otelTracer, error) {
	if cfg.ExporterURL == "" {
		return nil, fmt.Errorf("endpoint is missing in the otlp tracer configuration")
	}

	if cfg.ServiceName == "" {
		return nil, fmt.Errorf("service name is missing in the otlp tracer configuration")
	}

	u, err := url.Parse(cfg.ExporterURL)
	if err != nil {
		return nil, fmt.Errorf("invalid exporter URL: %w", err)
	}

	endpoint := u.Host
	if u.Scheme == "http" {
		cfg.Creds = nil
	}

	var secureOption otlptracegrpc.Option
	if cfg.Creds != nil {
		secureOption = otlptracegrpc.WithTLSCredentials(*cfg.Creds)
	} else {
		secureOption = otlptracegrpc.WithInsecure()
	}

	exporter, err := otlptrace.New(
		ctx,
		otlptracegrpc.NewClient(
			otlptracegrpc.WithEndpoint(endpoint),
			secureOption,
			otlptracegrpc.WithHeaders(map[string]string{
				"Authorization": fmt.Sprintf("Bearer %s", cfg.SecretToken),
			}),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create otlp exporter: %w", err)
	}

	resource, err := resource.New(
		ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			semconv.ServiceVersionKey.String(cfg.ServiceVersion),
			semconv.DeploymentEnvironmentKey.String(cfg.DeploymentEnvironment),
			semconv.TelemetrySDKLanguageKey.String("go"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create otlp resource: %w", err)
	}

	var sampler sdkTrace.Sampler = sdkTrace.AlwaysSample()
	if cfg.Sampler != nil {
		sampler = *cfg.Sampler
	}

	tp := sdkTrace.NewTracerProvider(
		sdkTrace.WithSampler(sampler),
		sdkTrace.WithBatcher(exporter),
		sdkTrace.WithResource(resource),
	)
	otel.SetTracerProvider(tp)

	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	return &otelTracer{
		tracer:         otel.Tracer(fmt.Sprintf("%s-tracer", cfg.ServiceName)),
		tracerProvider: tp,
	}, nil
}

func InitNoopTracer(ctx context.Context) (*otelTracer, error) {
	tp := noop.NewTracerProvider()
	otel.SetTracerProvider(tp)

	return &otelTracer{
		tracer:         otel.Tracer("noop-tracer"),
		tracerProvider: tp,
	}, nil
}

func (t *otelTracer) Tracer() trace.Tracer {
	if t.tracer != nil {
		return t.tracer
	}

	return nil
}

func (t *otelTracer) TracerProvider() trace.TracerProvider {
	if t.tracerProvider != nil {
		return t.tracerProvider
	}

	return nil
}

func (t *otelTracer) Shutdown(ctx context.Context) error {
	if tp, ok := t.tracerProvider.(*sdkTrace.TracerProvider); ok {
		if err := tp.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown tracer provider: %w", err)
		}
	}

	return nil
}
