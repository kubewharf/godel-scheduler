package adaptiveattenuation

type AdaptiveAttenuationParadigm interface {
	Name() string
	Predict(float64) float64
}

// ------------------------------------- DefaultAdaptiveAttenuationParadigm -------------------------------------

type DefaultAdaptiveAttenuationParadigm struct {
	B float64
}

var _ AdaptiveAttenuationParadigm = &DefaultAdaptiveAttenuationParadigm{}

func NewDefaultParadigm(b float64) AdaptiveAttenuationParadigm {
	return &DefaultAdaptiveAttenuationParadigm{b}
}

func (p *DefaultAdaptiveAttenuationParadigm) Name() string {
	return "default"
}

func (p *DefaultAdaptiveAttenuationParadigm) Predict(x float64) float64 {
	return p.B
}

// ------------------------------------- SquareAdaptiveAttenuationParadigm -------------------------------------

// Y = -1 * A * X^2 + B
type SquareAdaptiveAttenuationParadigm struct {
	A, B, x1, y1 float64
}

var _ AdaptiveAttenuationParadigm = &SquareAdaptiveAttenuationParadigm{}

func NewSquareParadigm(b, x1, y1 float64) AdaptiveAttenuationParadigm {
	var a float64
	if b <= y1 {
		a = 0
		y1 = b // ATTENTION
	} else {
		a = (b - y1) / (x1 * x1)
	}
	return &SquareAdaptiveAttenuationParadigm{a, b, x1, y1}
}

func (p *SquareAdaptiveAttenuationParadigm) Name() string {
	return "square"
}

func (p *SquareAdaptiveAttenuationParadigm) Predict(x float64) float64 {
	if x >= p.x1 {
		return p.y1
	}
	return -1*p.A*x*x + p.B
}
