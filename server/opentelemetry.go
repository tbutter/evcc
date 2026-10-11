package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/benbjohnson/clock"
	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/core/site"
	"github.com/evcc-io/evcc/util"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc/credentials"
)

type metricRecorder interface {
	Record(ctx context.Context, name string, val float64, attrs []attribute.KeyValue)
}

type otelRecorder struct {
	sync.Mutex
	meter  metric.Meter
	gauges map[string]metric.Float64Gauge
	log    *util.Logger
}

func (r *otelRecorder) Record(ctx context.Context, name string, val float64, attrs []attribute.KeyValue) {
	r.Lock()
	g, ok := r.gauges[name]
	if !ok {
		var err error
		g, err = r.meter.Float64Gauge(name)
		if err != nil {
			r.Unlock()
			r.log.ERROR.Printf("create gauge %s: %v", name, err)
			return
		}
		r.gauges[name] = g
	}
	r.Unlock()

	g.Record(ctx, val, metric.WithAttributes(attrs...))
}

// OpenTelemetry is an OpenTelemetry metrics publisher
type OpenTelemetry struct {
	sync.Mutex
	log           *util.Logger
	clock         clock.Clock
	meterProvider *sdkmetric.MeterProvider
	recorder      metricRecorder
	ctx           context.Context
	cancel        context.CancelFunc
}

// toDottedName transforms a camelCase or concatenated name to dotted notation:
// e.g. "gridPower" -> "evcc.grid.power", "pvExcessDCPower" -> "evcc.pv.excess.dc.power"
func toDottedName(key string) string {
	if strings.HasPrefix(key, "evcc.") {
		return key
	}

	key = strings.ReplaceAll(key, "KWh", "Kwh")

	var b strings.Builder
	b.WriteString("evcc.")
	runes := []rune(key)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if unicode.IsUpper(r) {
			if i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
				b.WriteByte('.')
			} else if i > 0 && i+1 < len(runes) && unicode.IsUpper(runes[i-1]) && unicode.IsLower(runes[i+1]) {
				b.WriteByte('.')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func otelTagValue(f reflect.StructField) string {
	if tag := tagValue("otel", f); tag != "" {
		return tag
	}
	return tagValue("influxdb", f)
}

// NewOpenTelemetryClient creates a new OpenTelemetry publisher
func NewOpenTelemetryClient(url, protocol string, headers map[string]string, insecure bool, serviceName string) (*OpenTelemetry, error) {
	log := util.NewLogger("otel")
	ctx, cancel := context.WithCancel(context.Background())

	if serviceName == "" {
		serviceName = "evcc"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String(util.FormattedVersion()),
		),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	endpoint := url
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	isGrpc := protocol == "grpc"

	// Normalize endpoint
	endpoint = strings.TrimPrefix(endpoint, "grpc://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	var exporter sdkmetric.Exporter
	if isGrpc {
		opts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(endpoint),
		}
		if len(headers) > 0 {
			opts = append(opts, otlpmetricgrpc.WithHeaders(headers))
		}
		if insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		} else {
			opts = append(opts, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(&tls.Config{})))
		}
		exporter, err = otlpmetricgrpc.New(ctx, opts...)
	} else {
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(endpoint),
		}
		if len(headers) > 0 {
			opts = append(opts, otlpmetrichttp.WithHeaders(headers))
		}
		if insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
			opts = append(opts, otlpmetrichttp.WithTLSClientConfig(&tls.Config{InsecureSkipVerify: true}))
		}
		exporter, err = otlpmetrichttp.New(ctx, opts...)
	}

	if err != nil {
		cancel()
		return nil, fmt.Errorf("otel exporter: %w", err)
	}

	reader := sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(5*time.Second))
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
	)

	recorder := &otelRecorder{
		meter:  provider.Meter("evcc"),
		gauges: make(map[string]metric.Float64Gauge),
		log:    log,
	}

	return &OpenTelemetry{
		log:           log,
		clock:         clock.New(),
		meterProvider: provider,
		recorder:      recorder,
		ctx:           ctx,
		cancel:        cancel,
	}, nil
}

// writeComplexMetric recursively records metrics matching the InfluxDB data mapping
func (m *OpenTelemetry) writeComplexMetric(ctx context.Context, key string, val any, attrs []attribute.KeyValue) {
	writeStruct := func(sv any) {
		typ := reflect.TypeOf(sv)
		v := reflect.ValueOf(sv)

		for i := range typ.NumField() {
			if f := typ.Field(i); f.IsExported() {
				if v.Field(i).IsZero() && jsonOmitEmpty(f) {
					continue
				}

				subKey := key + strings.ToUpper(f.Name[:1]) + f.Name[1:]
				if tag := otelTagValue(f); tag != "" {
					subKey = tag
				}

				fieldVal := v.Field(i).Interface()
				m.writeComplexMetric(ctx, subKey, fieldVal, attrs)
			}
		}
	}

	switch valueType := val.(type) {
	case string:
		return

	case int:
		metricName := toDottedName(key)
		m.recorder.Record(ctx, metricName, float64(valueType), attrs)

	case int64:
		metricName := toDottedName(key)
		m.recorder.Record(ctx, metricName, float64(valueType), attrs)

	case float64:
		metricName := toDottedName(key)
		m.recorder.Record(ctx, metricName, valueType, attrs)

	case []float64:
		if len(valueType) != 3 {
			return
		}
		metricName := toDottedName(key)
		for i, v := range valueType {
			phaseAttrs := slices.Clone(attrs)
			phaseAttrs = append(phaseAttrs, attribute.String("phase", fmt.Sprintf("l%d", i+1)))
			m.recorder.Record(ctx, metricName, v, phaseAttrs)
		}

	case [3]float64:
		metricName := toDottedName(key)
		for i, v := range valueType {
			phaseAttrs := slices.Clone(attrs)
			phaseAttrs = append(phaseAttrs, attribute.String("phase", fmt.Sprintf("l%d", i+1)))
			m.recorder.Record(ctx, metricName, v, phaseAttrs)
		}

	default:
		if val == nil {
			return
		}

		switch typ := reflect.TypeOf(val); {
		case typ.Kind() == reflect.Pointer:
			if v := reflect.ValueOf(val); !v.IsNil() {
				m.writeComplexMetric(ctx, key, reflect.Indirect(v).Interface(), attrs)
			}

		case typ.Kind() == reflect.Struct:
			writeStruct(val)

		case typ.Kind() == reflect.Slice && typ.Elem().Kind() == reflect.Struct:
			v := reflect.ValueOf(val)
			for i := range v.Len() {
				subAttrs := slices.Clone(attrs)
				subAttrs = append(subAttrs, attribute.String("id", strconv.Itoa(i+1)))

				ival := v.Index(i)
				if tp, ok := reflect.TypeAssert[api.TitleDescriber](ival); ok {
					if title := tp.GetTitle(); title != "" {
						subAttrs = append(subAttrs, attribute.String("title", title))
					}
				}
				m.writeComplexMetric(ctx, key, ival.Interface(), subAttrs)
			}
			return
		}
	}
}

// Run starts the OpenTelemetry publisher loop
func (m *OpenTelemetry) Run(site site.API, in <-chan util.Param) {
	for param := range in {
		var attrs []attribute.KeyValue
		if param.Loadpoint != nil {
			lp := site.Loadpoints()[*param.Loadpoint]
			if lp == nil {
				continue
			}

			attrs = append(attrs, attribute.String("loadpoint", lp.GetTitle()))
			if v := lp.GetVehicle(); v != nil {
				attrs = append(attrs, attribute.String("vehicle", v.GetTitle()))
			}
		}

		m.writeComplexMetric(m.ctx, param.Key, param.Val, attrs)
	}

	if m.meterProvider != nil {
		_ = m.meterProvider.Shutdown(context.Background())
	}
	if m.cancel != nil {
		m.cancel()
	}
}
