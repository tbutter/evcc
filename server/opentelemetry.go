package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/core/keys"
	"github.com/evcc-io/evcc/core/site"
	"github.com/evcc-io/evcc/core/types"
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

type metricDef struct {
	name        string
	unit        string
	description string
}

var (
	// Site & energy flow metrics
	defSiteHomePower            = metricDef{"evcc.site.home.power", "W", "Household power consumption"}
	defSiteGridPower            = metricDef{"evcc.site.grid.power", "W", "Grid power (positive = import, negative = export)"}
	defSiteGridEnergyImport     = metricDef{"evcc.site.grid.energy.import", "kWh", "Cumulative energy imported from grid"}
	defSiteGridEnergyExport     = metricDef{"evcc.site.grid.energy.export", "kWh", "Cumulative energy exported to grid"}
	defSiteGridPhasePower       = metricDef{"evcc.site.grid.phase.power", "W", "Per-phase grid power"}
	defSiteGridPhaseCurrent     = metricDef{"evcc.site.grid.phase.current", "A", "Per-phase grid current"}
	defSitePvPower              = metricDef{"evcc.site.pv.power", "W", "Total solar generation power"}
	defSitePvEnergy             = metricDef{"evcc.site.pv.energy", "kWh", "Total cumulative solar energy generated"}
	defSiteBatteryPower         = metricDef{"evcc.site.battery.power", "W", "Total home battery power (positive = discharging, negative = charging)"}
	defSiteBatterySoc           = metricDef{"evcc.site.battery.soc", "%", "Aggregated home battery state of charge"}
	defSiteBatteryEnergyDis     = metricDef{"evcc.site.battery.energy.discharged", "kWh", "Total cumulative energy discharged from battery"}
	defSiteBatteryEnergyCharged = metricDef{"evcc.site.battery.energy.charged", "kWh", "Total cumulative energy charged into battery"}
	defSiteAuxPower             = metricDef{"evcc.site.aux.power", "W", "Total auxiliary meter power"}
	defSiteGreenShareHome       = metricDef{"evcc.site.green_share.home", "%", "Share of green energy in home consumption"}
	defSiteGreenShareLoadpoints = metricDef{"evcc.site.green_share.loadpoints", "%", "Share of green energy used for charging"}

	// Individual device / sub-meter metrics
	defMeterPower        = metricDef{"evcc.meter.power", "W", "Device active power"}
	defMeterEnergyImport = metricDef{"evcc.meter.energy.import", "kWh", "Device cumulative energy"}
	defMeterEnergyExport = metricDef{"evcc.meter.energy.export", "kWh", "Device cumulative return energy"}
	defMeterPhasePower   = metricDef{"evcc.meter.phase.power", "W", "Device per-phase power"}
	defMeterPhaseCurrent = metricDef{"evcc.meter.phase.current", "A", "Device per-phase current"}
	defMeterSoc          = metricDef{"evcc.meter.soc", "%", "Battery device state of charge"}

	// Tariff & environment metrics
	defTariffPriceGrid       = metricDef{"evcc.tariff.price.grid", "{currency}/kWh", "Current grid import energy price"}
	defTariffPriceFeedIn     = metricDef{"evcc.tariff.price.feedin", "{currency}/kWh", "Current grid feed-in tariff"}
	defTariffPriceSolar      = metricDef{"evcc.tariff.price.solar", "{currency}/kWh", "Current solar tariff"}
	defTariffPriceHome       = metricDef{"evcc.tariff.price.home", "{currency}/kWh", "Effective energy price for home consumption"}
	defTariffPriceLoadpoints = metricDef{"evcc.tariff.price.loadpoints", "{currency}/kWh", "Effective energy price for charging"}
	defTariffCo2Grid         = metricDef{"evcc.tariff.co2.grid", "g/kWh", "Current grid CO2 intensity"}
	defTariffCo2Home         = metricDef{"evcc.tariff.co2.home", "g/kWh", "Effective CO2 intensity for home consumption"}
	defTariffCo2Loadpoints   = metricDef{"evcc.tariff.co2.loadpoints", "g/kWh", "Effective CO2 intensity for charging"}
	defTariffTemperature     = metricDef{"evcc.tariff.temperature", "Cel", "Outdoor temperature"}

	// Loadpoint & charging session metrics
	defLpConnected               = metricDef{"evcc.loadpoint.connected", "1", "Vehicle connected state (0 or 1)"}
	defLpCharging                = metricDef{"evcc.loadpoint.charging", "1", "Active charging state (0 or 1)"}
	defLpEnabled                 = metricDef{"evcc.loadpoint.enabled", "1", "Charger enabled state (0 or 1)"}
	defLpChargePower             = metricDef{"evcc.loadpoint.charge.power", "W", "Current charging power"}
	defLpChargeCurrent           = metricDef{"evcc.loadpoint.charge.current", "A", "Per-phase charging current"}
	defLpChargeVoltage           = metricDef{"evcc.loadpoint.charge.voltage", "V", "Per-phase charging voltage"}
	defLpOfferedCurrent          = metricDef{"evcc.loadpoint.offered.current", "A", "Offered current setpoint"}
	defLpPhasesActive            = metricDef{"evcc.loadpoint.phases.active", "{phase}", "Active charging phases"}
	defLpEnergyImport            = metricDef{"evcc.loadpoint.energy.import", "kWh", "Cumulative charger meter import reading"}
	defLpSessionEnergy           = metricDef{"evcc.loadpoint.session.energy", "kWh", "Energy charged in current session"}
	defLpSessionSolarShare       = metricDef{"evcc.loadpoint.session.solar_share", "%", "Solar share in current session"}
	defLpSessionPrice            = metricDef{"evcc.loadpoint.session.price", "{currency}", "Total cost of current charging session"}
	defLpSessionDuration         = metricDef{"evcc.loadpoint.session.duration", "s", "Charging duration of current session"}
	defLpConnectedDuration       = metricDef{"evcc.loadpoint.connected.duration", "s", "Duration since vehicle connected"}
	defLpChargeRemainingDuration = metricDef{"evcc.loadpoint.charge.remaining.duration", "s", "Estimated remaining charging time"}
	defLpChargeRemainingEnergy   = metricDef{"evcc.loadpoint.charge.remaining.energy", "kWh", "Estimated remaining energy to target"}

	// Vehicle metrics
	defVehicleSoc         = metricDef{"evcc.vehicle.soc", "%", "Vehicle state of charge"}
	defVehicleTemperature = metricDef{"evcc.vehicle.temperature", "Cel", "Heating device temperature"}
	defVehicleLimitSoc    = metricDef{"evcc.vehicle.limit.soc", "%", "Vehicle-side SoC limit"}
	defVehicleRange       = metricDef{"evcc.vehicle.range", "km", "Estimated vehicle range"}
	defVehicleOdometer    = metricDef{"evcc.vehicle.odometer", "km", "Vehicle odometer reading"}

	// Circuit metrics
	defCircuitPower      = metricDef{"evcc.circuit.power", "W", "Current circuit power"}
	defCircuitCurrent    = metricDef{"evcc.circuit.current", "A", "Current max phase current in circuit"}
	defCircuitMaxPower   = metricDef{"evcc.circuit.max.power", "W", "Configured maximum power limit for circuit"}
	defCircuitMaxCurrent = metricDef{"evcc.circuit.max.current", "A", "Configured maximum current limit for circuit"}
)

type metricRecorder interface {
	Record(ctx context.Context, def metricDef, val float64, attrs []attribute.KeyValue)
}

type otelRecorder struct {
	sync.Mutex
	meter  metric.Meter
	gauges map[string]metric.Float64Gauge
	log    *util.Logger
}

func (r *otelRecorder) Record(ctx context.Context, def metricDef, val float64, attrs []attribute.KeyValue) {
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return
	}

	r.Lock()
	g, ok := r.gauges[def.name]
	if !ok {
		var opts []metric.Float64GaugeOption
		if def.unit != "" {
			opts = append(opts, metric.WithUnit(def.unit))
		}
		if def.description != "" {
			opts = append(opts, metric.WithDescription(def.description))
		}

		var err error
		g, err = r.meter.Float64Gauge(def.name, opts...)
		if err != nil {
			r.Unlock()
			r.log.ERROR.Printf("create gauge %s: %v", def.name, err)
			return
		}
		r.gauges[def.name] = g
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
	heating       map[int]bool
	ctx           context.Context
	cancel        context.CancelFunc
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
		heating:       make(map[int]bool),
		ctx:           ctx,
		cancel:        cancel,
	}, nil
}

func toFloat64(val any) (float64, bool) {
	if val == nil {
		return 0, false
	}
	switch v := val.(type) {
	case float64:
		return v, true
	case *float64:
		if v != nil {
			return *v, true
		}
	case int:
		return float64(v), true
	case *int:
		if v != nil {
			return float64(*v), true
		}
	case int64:
		return float64(v), true
	case *int64:
		if v != nil {
			return float64(*v), true
		}
	case time.Duration:
		return v.Seconds(), true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	case *bool:
		if v != nil {
			if *v {
				return 1, true
			}
			return 0, true
		}
	}
	return 0, false
}

func toPhases(val any) ([]float64, bool) {
	switch v := val.(type) {
	case []float64:
		if len(v) == 3 {
			return v, true
		}
	case [3]float64:
		return v[:], true
	}
	return nil, false
}

func (m *OpenTelemetry) recordPhases(ctx context.Context, def metricDef, phases []float64, attrs []attribute.KeyValue) {
	for i, v := range phases {
		phaseAttrs := slices.Clone(attrs)
		phaseAttrs = append(phaseAttrs, attribute.String("phase", fmt.Sprintf("l%d", i+1)))
		m.recorder.Record(ctx, def, v, phaseAttrs)
	}
}

func (m *OpenTelemetry) recordGrid(ctx context.Context, val any) {
	var meas types.Measurement
	switch v := val.(type) {
	case types.Measurement:
		meas = v
	case *types.Measurement:
		if v == nil {
			return
		}
		meas = *v
	default:
		return
	}

	m.recorder.Record(ctx, defSiteGridPower, meas.Power, nil)
	if meas.Energy != nil {
		m.recorder.Record(ctx, defSiteGridEnergyImport, *meas.Energy, nil)
	}
	if meas.ReturnEnergy != nil {
		m.recorder.Record(ctx, defSiteGridEnergyExport, *meas.ReturnEnergy, nil)
	}
	if len(meas.Powers) == 3 {
		m.recordPhases(ctx, defSiteGridPhasePower, meas.Powers, nil)
	}
	if len(meas.Currents) == 3 {
		m.recordPhases(ctx, defSiteGridPhaseCurrent, meas.Currents, nil)
	}
}

func (m *OpenTelemetry) recordBattery(ctx context.Context, val any) {
	var bat types.BatteryState
	switch v := val.(type) {
	case types.BatteryState:
		bat = v
	case *types.BatteryState:
		if v == nil {
			return
		}
		bat = *v
	default:
		return
	}

	m.recorder.Record(ctx, defSiteBatteryPower, bat.Power, nil)
	m.recorder.Record(ctx, defSiteBatterySoc, bat.Soc, nil)
	if bat.Energy > 0 {
		m.recorder.Record(ctx, defSiteBatteryEnergyDis, bat.Energy, nil)
	}
	if bat.ReturnEnergy > 0 {
		m.recorder.Record(ctx, defSiteBatteryEnergyCharged, bat.ReturnEnergy, nil)
	}
	m.recordMeters(ctx, "battery", bat.Devices)
}

func (m *OpenTelemetry) recordMeters(ctx context.Context, kind string, val any) {
	list, ok := val.([]types.Measurement)
	if !ok {
		return
	}

	for i, dev := range list {
		attrs := []attribute.KeyValue{
			attribute.String("kind", kind),
			attribute.String("id", strconv.Itoa(i+1)),
		}
		if dev.Title != "" {
			attrs = append(attrs, attribute.String("title", dev.Title))
		}

		m.recorder.Record(ctx, defMeterPower, dev.Power, attrs)
		if dev.Energy != nil {
			m.recorder.Record(ctx, defMeterEnergyImport, *dev.Energy, attrs)
		}
		if dev.ReturnEnergy != nil {
			m.recorder.Record(ctx, defMeterEnergyExport, *dev.ReturnEnergy, attrs)
		}
		if len(dev.Powers) == 3 {
			m.recordPhases(ctx, defMeterPhasePower, dev.Powers, attrs)
		}
		if len(dev.Currents) == 3 {
			m.recordPhases(ctx, defMeterPhaseCurrent, dev.Currents, attrs)
		}
		if dev.Soc != nil {
			m.recorder.Record(ctx, defMeterSoc, *dev.Soc, attrs)
		}
	}
}

func (m *OpenTelemetry) recordCircuits(ctx context.Context, val any) {
	if val == nil {
		return
	}
	rv := reflect.ValueOf(val)
	if rv.Kind() != reflect.Map {
		return
	}

	mapKeys := rv.MapKeys()
	slices.SortFunc(mapKeys, func(a, b reflect.Value) int {
		return strings.Compare(a.String(), b.String())
	})

	for _, mk := range mapKeys {
		name := mk.String()
		entry := rv.MapIndex(mk)
		if entry.Kind() == reflect.Interface || entry.Kind() == reflect.Pointer {
			if entry.IsNil() {
				continue
			}
			entry = entry.Elem()
		}
		if entry.Kind() != reflect.Struct {
			continue
		}

		attrs := []attribute.KeyValue{attribute.String("circuit", name)}
		if f := entry.FieldByName("Title"); f.IsValid() && f.Kind() == reflect.String && f.String() != "" {
			attrs = append(attrs, attribute.String("title", f.String()))
		}

		if f := entry.FieldByName("Power"); f.IsValid() && f.CanFloat() {
			m.recorder.Record(ctx, defCircuitPower, f.Float(), attrs)
		}
		if f := entry.FieldByName("Current"); f.IsValid() && f.Kind() == reflect.Pointer && !f.IsNil() && f.Elem().CanFloat() {
			m.recorder.Record(ctx, defCircuitCurrent, f.Elem().Float(), attrs)
		}
		if f := entry.FieldByName("MaxPower"); f.IsValid() && f.CanFloat() && f.Float() > 0 {
			m.recorder.Record(ctx, defCircuitMaxPower, f.Float(), attrs)
		}
		if f := entry.FieldByName("MaxCurrent"); f.IsValid() && f.CanFloat() && f.Float() > 0 {
			m.recorder.Record(ctx, defCircuitMaxCurrent, f.Float(), attrs)
		}
	}
}

func (m *OpenTelemetry) recordParam(ctx context.Context, param util.Param, attrs []attribute.KeyValue) {
	if param.Loadpoint != nil {
		lpIdx := *param.Loadpoint
		switch param.Key {
		case keys.ChargerFeature + api.Heating.String():
			if b, ok := param.Val.(bool); ok {
				if m.heating == nil {
					m.heating = make(map[int]bool)
				}
				m.heating[lpIdx] = b
			}
		case keys.Connected:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpConnected, v, attrs)
			}
		case keys.Charging:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpCharging, v, attrs)
			}
		case keys.Enabled:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpEnabled, v, attrs)
			}
		case keys.ChargePower:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpChargePower, v, attrs)
			}
		case keys.ChargeCurrents:
			if phases, ok := toPhases(param.Val); ok {
				m.recordPhases(ctx, defLpChargeCurrent, phases, attrs)
			}
		case keys.ChargeVoltages:
			if phases, ok := toPhases(param.Val); ok {
				m.recordPhases(ctx, defLpChargeVoltage, phases, attrs)
			}
		case keys.OfferedCurrent:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpOfferedCurrent, v, attrs)
			}
		case keys.PhasesActive:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpPhasesActive, v, attrs)
			}
		case keys.ChargeTotalImport:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpEnergyImport, v, attrs)
			}
		case "sessionEnergy":
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpSessionEnergy, v/1e3, attrs)
			}
		case "sessionSolarPercentage":
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpSessionSolarShare, v, attrs)
			}
		case "sessionPrice":
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpSessionPrice, v, attrs)
			}
		case keys.ChargeDuration:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpSessionDuration, v, attrs)
			}
		case keys.ConnectedDuration:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpConnectedDuration, v, attrs)
			}
		case keys.ChargeRemainingDuration:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpChargeRemainingDuration, v, attrs)
			}
		case keys.ChargeRemainingEnergy:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defLpChargeRemainingEnergy, v/1e3, attrs)
			}
		case keys.VehicleSoc:
			if v, ok := toFloat64(param.Val); ok {
				if m.heating[lpIdx] {
					m.recorder.Record(ctx, defVehicleTemperature, v, attrs)
				} else {
					m.recorder.Record(ctx, defVehicleSoc, v, attrs)
				}
			}
		case keys.VehicleLimitSoc:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defVehicleLimitSoc, v, attrs)
			}
		case keys.VehicleRange:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defVehicleRange, v, attrs)
			}
		case keys.VehicleOdometer:
			if v, ok := toFloat64(param.Val); ok {
				m.recorder.Record(ctx, defVehicleOdometer, v, attrs)
			}
		}
		return
	}

	switch param.Key {
	case keys.HomePower:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defSiteHomePower, v, nil)
		}
	case keys.Grid:
		m.recordGrid(ctx, param.Val)
	case keys.PvPower:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defSitePvPower, v, nil)
		}
	case keys.PvEnergy:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defSitePvEnergy, v, nil)
		}
	case keys.Battery:
		m.recordBattery(ctx, param.Val)
	case keys.AuxPower:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defSiteAuxPower, v, nil)
		}
	case keys.GreenShareHome:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defSiteGreenShareHome, v*100, nil)
		}
	case keys.GreenShareLoadpoints:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defSiteGreenShareLoadpoints, v*100, nil)
		}
	case keys.Pv:
		m.recordMeters(ctx, "pv", param.Val)
	case keys.Aux:
		m.recordMeters(ctx, "aux", param.Val)
	case keys.Ext:
		m.recordMeters(ctx, "ext", param.Val)
	case keys.Consumers:
		m.recordMeters(ctx, "consumer", param.Val)
	case keys.TariffGrid:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defTariffPriceGrid, v, nil)
		}
	case keys.TariffFeedIn:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defTariffPriceFeedIn, v, nil)
		}
	case keys.TariffSolar:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defTariffPriceSolar, v, nil)
		}
	case keys.TariffPriceHome:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defTariffPriceHome, v, nil)
		}
	case keys.TariffPriceLoadpoints:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defTariffPriceLoadpoints, v, nil)
		}
	case keys.TariffCo2:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defTariffCo2Grid, v, nil)
		}
	case keys.TariffCo2Home:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defTariffCo2Home, v, nil)
		}
	case keys.TariffCo2Loadpoints:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defTariffCo2Loadpoints, v, nil)
		}
	case keys.TariffTemperature:
		if v, ok := toFloat64(param.Val); ok {
			m.recorder.Record(ctx, defTariffTemperature, v, nil)
		}
	case keys.Circuits:
		m.recordCircuits(ctx, param.Val)
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

		m.recordParam(m.ctx, param, attrs)
	}

	if m.meterProvider != nil {
		_ = m.meterProvider.Shutdown(context.Background())
	}
	if m.cancel != nil {
		m.cancel()
	}
}
