package adaptiveattenuation

import (
	"reflect"
	"testing"
)

func TestDefaultAdaptiveAttenuationParadigm_Predict(t *testing.T) {
	type fields struct {
		B float64
	}
	tests := []struct {
		name   string
		fields fields
		args   []float64
		want   []float64
	}{
		{
			name:   "B=51",
			fields: fields{51},
			args:   []float64{10, 100, 300, 500, 1000, 5000, 10000},
		},
		{
			name:   "B=11",
			fields: fields{11},
			args:   []float64{10, 100, 300, 500, 1000, 5000, 10000},
		},
		{
			name:   "B=2001",
			fields: fields{2001},
			args:   []float64{10, 100, 300, 500, 1000, 5000, 10000},
		},
		{
			name:   "B=2001",
			fields: fields{2001},
			args:   []float64{10, 100, 300, 500, 1000, 5000, 10000},
		},
		{
			name:   "B=1001",
			fields: fields{1001},
			args:   []float64{10, 100, 300, 500, 1000, 5000, 10000},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewDefaultParadigm(tt.fields.B)

			for _, x := range tt.args {
				got := p.Predict(x)
				if got != tt.fields.B {
					t.Errorf("AdaptiveAttenuationParadigm.Predict(%v) = %v, want %v", x, got, tt.want)
				}
			}
		})
	}
}

func TestSquareAdaptiveAttenuationParadigm_Predict(t *testing.T) {
	type fields struct {
		B  float64
		x1 float64
		y1 float64
	}
	tests := []struct {
		name   string
		fields fields
		args   []float64
		want   []float64
	}{
		{
			name:   "B=51, x1=500, y1=1",
			fields: fields{51, 500, 1},
			args:   []float64{10, 100, 300, 500, 1000, 5000, 10000},
			want:   []float64{50.98, 49, 33, 1, 1, 1, 1},
		},
		{
			name:   "B=11, x1=1000, y1=1",
			fields: fields{11, 1000, 1},
			args:   []float64{10, 100, 300, 500, 1000, 5000, 10000},
			want:   []float64{10.999, 10.9, 10.1, 8.5, 1, 1, 1},
		},
		{
			name:   "B=2001, x1=500, y1=1",
			fields: fields{2001, 500, 1},
			args:   []float64{10, 100, 300, 500, 1000, 5000, 10000},
			want:   []float64{2000.2, 1921, 1281, 1, 1, 1, 1},
		},
		{
			name:   "B=2001, x1=500, y1=2011",
			fields: fields{2001, 500, 2011},
			args:   []float64{10, 100, 300, 500, 1000, 5000, 10000},
			want:   []float64{2001, 2001, 2001, 2001, 2001, 2001, 2001},
		},
		{
			name:   "B=1001, x1=500, y1=2011",
			fields: fields{1001, 500, 2011},
			args:   []float64{10, 100, 300, 500, 1000, 5000, 10000},
			want:   []float64{1001, 1001, 1001, 1001, 1001, 1001, 1001},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewSquareParadigm(tt.fields.B, tt.fields.x1, tt.fields.y1)

			got := []float64{}
			for _, x := range tt.args {
				got = append(got, p.Predict(x))
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AdaptiveAttenuationParadigm.Predict() = %v, want %v", got, tt.want)
			}
		})
	}
}
