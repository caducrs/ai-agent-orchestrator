package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

																						func Setup(ctx context.Context, serviceName, version, endpoint string) (func(context.Context) error, error) {
																							otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
																							if endpoint == "" {
																								return func(context.Context) error { return nil }, nil
																							}
																							exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
																							if err != nil {
																								return nil, err
																							}
																							serviceResource := resource.NewWithAttributes("", attribute.String("service.name", serviceName), attribute.String("service.version", version))
																							provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(serviceResource), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())))
																							otel.SetTracerProvider(provider)
																							return provider.Shutdown, nil
																						}
