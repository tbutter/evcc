package server

import (
	"context"
	"testing"

	"github.com/benbjohnson/clock"
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
	Val   float64
	Attrs []attribute.KeyValue
}

type testRecorder struct {
	records []recordedMetric
}

func (r *testRecorder) Record(ctx context.Context, name string, val float64, attrs []attribute.KeyValue) {
	r.records = append(r.records, recordedMetric{
		Name:  name,
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
		ctx:      context.Background(),
	}
}

func (suite *otelSuite) SetupTest() {
	suite.recorder.records = nil
}

func (suite *otelSuite) WriteParam(p util.Param) {
	var attrs []attribute.KeyValue
	suite.OpenTelemetry.writeComplexMetric(suite.ctx, p.Key, p.Val, attrs)
}

func (w *otelSuite) TestToDottedName() {
	w.Equal("evcc.charge.power", toDottedName("chargePower"))
	w.Equal("evcc.grid.power", toDottedName("gridPower"))
	w.Equal("evcc.battery.soc", toDottedName("batterySoc"))
	w.Equal("evcc.pv.excess.dc.power", toDottedName("pvExcessDCPower"))
	w.Equal("evcc.tariff.price.home", toDottedName("tariffPriceHome"))
	w.Equal("evcc.session.co2.per.kwh", toDottedName("sessionCo2PerKWh"))
	w.Equal("evcc.foo", toDottedName("foo"))
	w.Equal("evcc.foo", toDottedName("evcc.foo"))
}

func (w *otelSuite) TestNumeric() {
	w.WriteParam(util.Param{Key: "gridPower", Val: 1500.5})
	w.Equal([]recordedMetric{
		{Name: "evcc.grid.power", Val: 1500.5, Attrs: nil},
	}, w.recorder.records)
}

func (w *otelSuite) TestStringIgnored() {
	w.WriteParam(util.Param{Key: "foo", Val: "hello"})
	w.Empty(w.recorder.records)
}

func (w *otelSuite) TestBoolIgnored() {
	w.WriteParam(util.Param{Key: "foo", Val: true})
	w.Empty(w.recorder.records)
}

func (w *otelSuite) TestNilIgnored() {
	w.WriteParam(util.Param{Key: "foo", Val: nil})
	w.Empty(w.recorder.records)
}

func (w *otelSuite) TestPointer() {
	val := 42.0
	w.WriteParam(util.Param{Key: "chargePower", Val: &val})
	w.Equal([]recordedMetric{
		{Name: "evcc.charge.power", Val: 42.0, Attrs: nil},
	}, w.recorder.records)
}

func (w *otelSuite) TestArrayPhases() {
	w.WriteParam(util.Param{Key: "chargeCurrents", Val: [3]float64{10, 16, 16}})
	w.Equal([]recordedMetric{
		{Name: "evcc.charge.currents", Val: 10, Attrs: []attribute.KeyValue{attribute.String("phase", "l1")}},
		{Name: "evcc.charge.currents", Val: 16, Attrs: []attribute.KeyValue{attribute.String("phase", "l2")}},
		{Name: "evcc.charge.currents", Val: 16, Attrs: []attribute.KeyValue{attribute.String("phase", "l3")}},
	}, w.recorder.records)
}

func (w *otelSuite) TestSlicePhases() {
	w.WriteParam(util.Param{Key: "chargeVoltages", Val: []float64{230, 231, 229}})
	w.Equal([]recordedMetric{
		{Name: "evcc.charge.voltages", Val: 230, Attrs: []attribute.KeyValue{attribute.String("phase", "l1")}},
		{Name: "evcc.charge.voltages", Val: 231, Attrs: []attribute.KeyValue{attribute.String("phase", "l2")}},
		{Name: "evcc.charge.voltages", Val: 229, Attrs: []attribute.KeyValue{attribute.String("phase", "l3")}},
	}, w.recorder.records)
}

func (w *otelSuite) TestSliceNotThreeIgnored() {
	w.WriteParam(util.Param{Key: "foo", Val: []float64{1, 2, 3, 4}})
	w.Empty(w.recorder.records)
}

func (w *otelSuite) TestMeasurement() {
	socVal := 80.0
	w.WriteParam(util.Param{Key: "battery", Val: types.Measurement{Power: 2500, Soc: &socVal}})
	w.Equal([]recordedMetric{
		{Name: "evcc.battery.power", Val: 2500, Attrs: nil},
		{Name: "evcc.battery.soc", Val: 80, Attrs: nil},
	}, w.recorder.records)
}

func (w *otelSuite) TestSliceOfStruct() {
	soc1, soc2 := 50.0, 75.0
	w.WriteParam(util.Param{Key: "grid", Val: []types.Measurement{
		{Title: "Main Grid", Power: 3000, Soc: &soc1},
		{Title: "Sub Grid", Power: 1000, Soc: &soc2},
	}})
	w.Equal([]recordedMetric{
		{Name: "evcc.grid.power", Val: 3000, Attrs: []attribute.KeyValue{attribute.String("id", "1"), attribute.String("title", "Main Grid")}},
		{Name: "evcc.grid.soc", Val: 50, Attrs: []attribute.KeyValue{attribute.String("id", "1"), attribute.String("title", "Main Grid")}},
		{Name: "evcc.grid.power", Val: 1000, Attrs: []attribute.KeyValue{attribute.String("id", "2"), attribute.String("title", "Sub Grid")}},
		{Name: "evcc.grid.soc", Val: 75, Attrs: []attribute.KeyValue{attribute.String("id", "2"), attribute.String("title", "Sub Grid")}},
	}, w.recorder.records)
}

func (w *otelSuite) TestBatteryState() {
	socDev := 90.0
	w.WriteParam(util.Param{Key: "battery", Val: types.BatteryState{
		Power: 5000,
		Soc:   90.0,
		Devices: []types.Measurement{{
			Title: "Home Battery 1",
			Power: 2500,
			Soc:   &socDev,
		}},
	}})
	w.Equal([]recordedMetric{
		{Name: "evcc.battery.power", Val: 5000, Attrs: nil},
		{Name: "evcc.battery.soc", Val: 90, Attrs: nil},
		{Name: "evcc.battery.power", Val: 2500, Attrs: []attribute.KeyValue{attribute.String("id", "1"), attribute.String("title", "Home Battery 1")}},
		{Name: "evcc.battery.soc", Val: 90, Attrs: []attribute.KeyValue{attribute.String("id", "1"), attribute.String("title", "Home Battery 1")}},
	}, w.recorder.records)
}
