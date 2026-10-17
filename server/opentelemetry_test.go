package server

import (
	"context"
	"testing"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/evcc-io/evcc/core/keys"
	"github.com/evcc-io/evcc/core/types"
	"github.com/evcc-io/evcc/util"
	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel/attribute"
)

func TestOpenTelemetryTypes(t *testing.T) {
	suite.Run(t, new(otelSuite))
}

type recordedMetric struct {
	Name  string
	Unit  string
	Val   float64
	Attrs []attribute.KeyValue
}

type testRecorder struct {
	records []recordedMetric
}

func (r *testRecorder) Record(_ context.Context, def metricDef, val float64, attrs []attribute.KeyValue) {
	r.records = append(r.records, recordedMetric{
		Name:  def.name,
		Unit:  def.unit,
		Val:   val,
		Attrs: attrs,
	})
}

type otelSuite struct {
	suite.Suite
	*OpenTelemetry
	recorder *testRecorder
}

func (suite *otelSuite) SetupSuite() {
	suite.recorder = new(testRecorder)
	suite.OpenTelemetry = &OpenTelemetry{
		log:      util.NewLogger("foo"),
		clock:    clock.NewMock(),
		recorder: suite.recorder,
		heating:  make(map[int]bool),
		ctx:      context.Background(),
	}
}

func (suite *otelSuite) SetupTest() {
	suite.recorder.records = nil
	clear(suite.OpenTelemetry.heating)
}

func (suite *otelSuite) WriteParam(p util.Param) {
	var attrs []attribute.KeyValue
	if p.Loadpoint != nil {
		attrs = append(attrs, attribute.String("loadpoint", "Garage"))
	}
	suite.OpenTelemetry.recordParam(suite.ctx, p, attrs)
}

func (w *otelSuite) TestSiteAndGridMetrics() {
	imp := 123.4
	exp := 56.7
	w.WriteParam(util.Param{Key: keys.HomePower, Val: 850.0})
	w.WriteParam(util.Param{Key: keys.Grid, Val: types.Measurement{
		Power:        1500.5,
		Energy:       &imp,
		ReturnEnergy: &exp,
		Powers:       []float64{500, 500, 500.5},
		Currents:     []float64{2.1, 2.2, 2.3},
	}})

	w.Equal([]recordedMetric{
		{Name: "evcc.site.home.power", Unit: "W", Val: 850.0, Attrs: nil},
		{Name: "evcc.site.grid.power", Unit: "W", Val: 1500.5, Attrs: nil},
		{Name: "evcc.site.grid.energy.import", Unit: "kWh", Val: 123.4, Attrs: nil},
		{Name: "evcc.site.grid.energy.export", Unit: "kWh", Val: 56.7, Attrs: nil},
		{Name: "evcc.site.grid.phase.power", Unit: "W", Val: 500, Attrs: []attribute.KeyValue{attribute.String("phase", "l1")}},
		{Name: "evcc.site.grid.phase.power", Unit: "W", Val: 500, Attrs: []attribute.KeyValue{attribute.String("phase", "l2")}},
		{Name: "evcc.site.grid.phase.power", Unit: "W", Val: 500.5, Attrs: []attribute.KeyValue{attribute.String("phase", "l3")}},
		{Name: "evcc.site.grid.phase.current", Unit: "A", Val: 2.1, Attrs: []attribute.KeyValue{attribute.String("phase", "l1")}},
		{Name: "evcc.site.grid.phase.current", Unit: "A", Val: 2.2, Attrs: []attribute.KeyValue{attribute.String("phase", "l2")}},
		{Name: "evcc.site.grid.phase.current", Unit: "A", Val: 2.3, Attrs: []attribute.KeyValue{attribute.String("phase", "l3")}},
	}, w.recorder.records)
}

func (w *otelSuite) TestGreenShareNormalizedToPercent() {
	w.WriteParam(util.Param{Key: keys.GreenShareHome, Val: 0.75})
	w.WriteParam(util.Param{Key: keys.GreenShareLoadpoints, Val: 0.42})

	w.Equal([]recordedMetric{
		{Name: "evcc.site.green_share.home", Unit: "%", Val: 75, Attrs: nil},
		{Name: "evcc.site.green_share.loadpoints", Unit: "%", Val: 42, Attrs: nil},
	}, w.recorder.records)
}

func (w *otelSuite) TestSeparatedSiteAndDeviceMeters() {
	socDev := 90.0
	pvEnergy := 45.6
	w.WriteParam(util.Param{Key: keys.PvPower, Val: 4000.0})
	w.WriteParam(util.Param{Key: keys.Pv, Val: []types.Measurement{
		{Title: "Roof East", Power: 2500, Energy: &pvEnergy},
		{Title: "Roof West", Power: 1500},
	}})
	w.WriteParam(util.Param{Key: keys.Battery, Val: types.BatteryState{
		Power: 5000,
		Soc:   85.0,
		Devices: []types.Measurement{{
			Title: "Home Battery 1",
			Power: 5000,
			Soc:   &socDev,
		}},
	}})

	w.Equal([]recordedMetric{
		{Name: "evcc.site.pv.power", Unit: "W", Val: 4000, Attrs: nil},
		{Name: "evcc.meter.power", Unit: "W", Val: 2500, Attrs: []attribute.KeyValue{attribute.String("kind", "pv"), attribute.String("id", "1"), attribute.String("title", "Roof East")}},
		{Name: "evcc.meter.energy.import", Unit: "kWh", Val: 45.6, Attrs: []attribute.KeyValue{attribute.String("kind", "pv"), attribute.String("id", "1"), attribute.String("title", "Roof East")}},
		{Name: "evcc.meter.power", Unit: "W", Val: 1500, Attrs: []attribute.KeyValue{attribute.String("kind", "pv"), attribute.String("id", "2"), attribute.String("title", "Roof West")}},
		{Name: "evcc.site.battery.power", Unit: "W", Val: 5000, Attrs: nil},
		{Name: "evcc.site.battery.soc", Unit: "%", Val: 85, Attrs: nil},
		{Name: "evcc.meter.power", Unit: "W", Val: 5000, Attrs: []attribute.KeyValue{attribute.String("kind", "battery"), attribute.String("id", "1"), attribute.String("title", "Home Battery 1")}},
		{Name: "evcc.meter.soc", Unit: "%", Val: 90, Attrs: []attribute.KeyValue{attribute.String("kind", "battery"), attribute.String("id", "1"), attribute.String("title", "Home Battery 1")}},
	}, w.recorder.records)
}

func (w *otelSuite) TestLoadpointSessionAndUnitNormalization() {
	lpIdx := 0
	lpAttrs := []attribute.KeyValue{attribute.String("loadpoint", "Garage")}

	w.WriteParam(util.Param{Loadpoint: &lpIdx, Key: keys.Connected, Val: true})
	w.WriteParam(util.Param{Loadpoint: &lpIdx, Key: keys.Charging, Val: false})
	w.WriteParam(util.Param{Loadpoint: &lpIdx, Key: keys.ChargePower, Val: 11000.0})
	w.WriteParam(util.Param{Loadpoint: &lpIdx, Key: keys.ChargeCurrents, Val: [3]float64{16, 16, 16}})
	w.WriteParam(util.Param{Loadpoint: &lpIdx, Key: "sessionEnergy", Val: 15500.0})          // raw Wh -> 15.5 kWh
	w.WriteParam(util.Param{Loadpoint: &lpIdx, Key: keys.ChargeRemainingEnergy, Val: 4500.0}) // raw Wh -> 4.5 kWh
	w.WriteParam(util.Param{Loadpoint: &lpIdx, Key: keys.ChargeDuration, Val: 30 * time.Minute})
	w.WriteParam(util.Param{Loadpoint: &lpIdx, Key: "sessionSolarPercentage", Val: 82.5})

	w.Equal([]recordedMetric{
		{Name: "evcc.loadpoint.connected", Unit: "1", Val: 1, Attrs: lpAttrs},
		{Name: "evcc.loadpoint.charging", Unit: "1", Val: 0, Attrs: lpAttrs},
		{Name: "evcc.loadpoint.charge.power", Unit: "W", Val: 11000, Attrs: lpAttrs},
		{Name: "evcc.loadpoint.charge.current", Unit: "A", Val: 16, Attrs: []attribute.KeyValue{attribute.String("loadpoint", "Garage"), attribute.String("phase", "l1")}},
		{Name: "evcc.loadpoint.charge.current", Unit: "A", Val: 16, Attrs: []attribute.KeyValue{attribute.String("loadpoint", "Garage"), attribute.String("phase", "l2")}},
		{Name: "evcc.loadpoint.charge.current", Unit: "A", Val: 16, Attrs: []attribute.KeyValue{attribute.String("loadpoint", "Garage"), attribute.String("phase", "l3")}},
		{Name: "evcc.loadpoint.session.energy", Unit: "kWh", Val: 15.5, Attrs: lpAttrs},
		{Name: "evcc.loadpoint.charge.remaining.energy", Unit: "kWh", Val: 4.5, Attrs: lpAttrs},
		{Name: "evcc.loadpoint.session.duration", Unit: "s", Val: 1800, Attrs: lpAttrs},
		{Name: "evcc.loadpoint.session.solar_share", Unit: "%", Val: 82.5, Attrs: lpAttrs},
	}, w.recorder.records)
}

func (w *otelSuite) TestVehicleSocVsHeatingTemperature() {
	lp0 := 0
	lp1 := 1
	lpAttrs := []attribute.KeyValue{attribute.String("loadpoint", "Garage")}

	// Standard EV loadpoint (not heating)
	w.WriteParam(util.Param{Loadpoint: &lp0, Key: keys.VehicleSoc, Val: 78.0})

	// Heating loadpoint
	w.WriteParam(util.Param{Loadpoint: &lp1, Key: "chargerFeatureHeating", Val: true})
	w.WriteParam(util.Param{Loadpoint: &lp1, Key: keys.VehicleSoc, Val: 52.5})

	w.Equal([]recordedMetric{
		{Name: "evcc.vehicle.soc", Unit: "%", Val: 78.0, Attrs: lpAttrs},
		{Name: "evcc.vehicle.temperature", Unit: "Cel", Val: 52.5, Attrs: lpAttrs},
	}, w.recorder.records)
}

func (w *otelSuite) TestCircuitsMap() {
	type testCircuit struct {
		Title      string
		Power      float64
		Current    *float64
		MaxPower   float64
		MaxCurrent float64
	}
	cur := 15.5
	w.WriteParam(util.Param{Key: keys.Circuits, Val: map[string]testCircuit{
		"main": {
			Title:      "Main Circuit",
			Power:      10500,
			Current:    &cur,
			MaxPower:   22000,
			MaxCurrent: 32,
		},
	}})

	cAttrs := []attribute.KeyValue{attribute.String("circuit", "main"), attribute.String("title", "Main Circuit")}
	w.Equal([]recordedMetric{
		{Name: "evcc.circuit.power", Unit: "W", Val: 10500, Attrs: cAttrs},
		{Name: "evcc.circuit.current", Unit: "A", Val: 15.5, Attrs: cAttrs},
		{Name: "evcc.circuit.max.power", Unit: "W", Val: 22000, Attrs: cAttrs},
		{Name: "evcc.circuit.max.current", Unit: "A", Val: 32, Attrs: cAttrs},
	}, w.recorder.records)
}

func (w *otelSuite) TestNonCuratedKeysIgnored() {
	w.WriteParam(util.Param{Key: keys.Network, Val: struct{ Port int }{Port: 7070}})
	w.WriteParam(util.Param{Key: "evopt", Val: struct{ Val float64 }{Val: 123}})
	w.WriteParam(util.Param{Key: "unknownMetric", Val: 42.0})
	w.Empty(w.recorder.records)
}
