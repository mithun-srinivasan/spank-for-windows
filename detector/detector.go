package detector

import "time"

type Sample struct {
	Amplitude float64
	T         time.Time
}

type Config struct {
	MinAmplitude float64
	SpikeRatio   float64
	AvgWindow    int
}

func DefaultConfig() Config {
	return Config{MinAmplitude: 0.05, SpikeRatio: 4.0, AvgWindow: 20}
}

type Detector struct {
	cfg    Config
	buf    []float64
	maxBuf int
}

func NewDetector(cfg Config) *Detector {
	mb := cfg.AvgWindow * 4
	if mb < 100 {
		mb = 100
	}
	return &Detector{cfg: cfg, buf: make([]float64, 0, mb), maxBuf: mb}
}

func (d *Detector) AddSample(s Sample) bool {
	amp := s.Amplitude
	d.buf = append(d.buf, amp)
	if len(d.buf) > d.maxBuf {
		d.buf = d.buf[len(d.buf)-d.maxBuf:]
	}
	if len(d.buf) < d.cfg.AvgWindow+1 || amp < d.cfg.MinAmplitude {
		return false
	}
	n := len(d.buf)
	var sum float64
	for _, v := range d.buf[n-d.cfg.AvgWindow-1 : n-1] {
		sum += v
	}
	bg := sum / float64(d.cfg.AvgWindow)
	if bg < 0.001 {
		bg = 0.001
	}
	return (amp / bg) >= d.cfg.SpikeRatio
}
