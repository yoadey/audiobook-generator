package tts

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// pcmFromWAV converts a WAV file (PCM 8/16/24/32-bit or 32-bit float, any
// rate and channel count) to SampleRate mono signed 16-bit PCM, scaled by
// gain but never louder than just below full scale.
func pcmFromWAV(data []byte, gain float64) ([]byte, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	var format, channels, bits uint16
	var rate uint32
	var samples []byte
	for at := 12; at+8 <= len(data); {
		id, size := string(data[at:at+4]), int(binary.LittleEndian.Uint32(data[at+4:at+8]))
		body := data[at+8:]
		// Streamed WAVs leave the data size open (0 or 0xFFFFFFFF).
		if size > len(body) || (id == "data" && size == 0) {
			size = len(body)
		}
		body = body[:size]
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("WAV: short fmt chunk")
			}
			format, channels = binary.LittleEndian.Uint16(body), binary.LittleEndian.Uint16(body[2:])
			rate, bits = binary.LittleEndian.Uint32(body[4:]), binary.LittleEndian.Uint16(body[14:])
			if format == 0xFFFE && size >= 26 { // WAVE_FORMAT_EXTENSIBLE: the sub-format
				format = binary.LittleEndian.Uint16(body[24:])
			}
		case "data":
			samples = body
		}
		at += 8 + size + size%2
	}
	if channels == 0 || rate == 0 || samples == nil {
		return nil, errors.New("WAV: no format or data")
	}
	width := int(bits) / 8
	if (format != 1 && format != 3) || (format == 3 && bits != 32) || width < 1 || width > 4 {
		return nil, fmt.Errorf("WAV: unsupported format %d with %d bits", format, bits)
	}
	frame := width * int(channels)
	n := len(samples) / frame
	mono := make([]float64, n)
	for i := range n {
		sum := 0.0
		for c := range int(channels) {
			sum += sample(samples[i*frame+c*width:], width, format == 3)
		}
		mono[i] = sum / float64(channels)
	}
	mono = resample(mono, int(rate), SampleRate)
	peak := 0.0
	for _, v := range mono {
		peak = max(peak, math.Abs(v))
	}
	if peak > 0 {
		gain = min(gain, 0.97/peak)
	}
	samples16 := make([]int16, len(mono))
	for i, v := range mono {
		samples16[i] = int16(math.Round(max(min(v*gain, 1), -1) * math.MaxInt16))
	}
	out := make([]byte, 2*len(samples16))
	if _, err := binary.Encode(out, binary.LittleEndian, samples16); err != nil {
		return nil, err
	}
	return out, nil
}

// sample reads one sample as -1..1.
func sample(b []byte, width int, float bool) float64 {
	if float {
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	}
	if width == 1 { // unsigned
		return (float64(b[0]) - 128) / 128
	}
	// Signed little-endian of 2-4 bytes.
	var v int64
	for i := width - 1; i >= 0; i-- {
		v = v<<8 | int64(b[i])
	}
	full := int64(1) << (8*width - 1)
	if v >= full {
		v -= 2 * full
	}
	return float64(v) / float64(full)
}

// resample converts between rates by linear interpolation (speech from
// 22.05-48 kHz to 24 kHz; good enough for voices).
func resample(in []float64, from, to int) []float64 {
	if from == to || len(in) == 0 {
		return in
	}
	n := int(int64(len(in)) * int64(to) / int64(from))
	out := make([]float64, n)
	step := float64(from) / float64(to)
	for i := range out {
		x := float64(i) * step
		j := int(x)
		if j+1 >= len(in) {
			out[i] = in[len(in)-1]
			continue
		}
		f := x - float64(j)
		out[i] = in[j]*(1-f) + in[j+1]*f
	}
	return out
}
