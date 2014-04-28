package mp3

import (
	"fmt"
	"io"
	"math"

	"github.com/mjibson/mog/codec"
)

func init() {
	codec.RegisterCodec("MP3", "\u00ff\u00fa", ReadMP3)
	codec.RegisterCodec("MP3", "\u00ff\u00fb", ReadMP3)
}

func ReadMP3(r io.Reader) ([]codec.Song, error) {
	m, err := New(r)
	if err != nil {
		return nil, err
	}
	if n, err := m.frame(); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, fmt.Errorf("mp3: cannot decode")
	}
	return []codec.Song{m}, nil
}

type MP3 struct {
	b *bitReader

	syncword           uint16
	ID                 byte
	layer              Layer
	protection_bit     byte
	bitrate_index      byte
	sampling_frequency byte
	padding_bit        byte
	private_bit        byte
	mode               Mode
	mode_extension     byte
	copyright          byte
	original_home      byte
	emphasis           Emphasis

	ov      [2][32][18]float64
	samples []float32
	V       [1024]float64
}

func New(r io.Reader) (*MP3, error) {
	m := MP3{
		b: newBitReader(r),
	}

	return &m, nil
}

func (m *MP3) Play(samples int) []float32 {
	for len(m.samples) < samples {
		if n, err := m.frame(); err != nil {
			// todo: return err
			return nil
		} else if n == 0 {
			break
		}
	}
	if samples > len(m.samples) {
		samples = len(m.samples)
	}
	r := m.samples[:samples]
	m.samples = m.samples[samples:]
	return r
}

func (m *MP3) Close() {
}

func (m *MP3) Info() codec.SongInfo {
	return codec.SongInfo{
		SampleRate: m.sampling(),
		Channels:   m.channels(),
	}
}

func (m *MP3) frame() (read int, err error) {
	if err := m.header(); err != nil {
		return 0, err
	}
	m.error_check()
	s := m.audio_data()
	m.samples = append(m.samples, s...)
	return len(s), nil
}

func (m *MP3) header() error {
	syncword := uint16(m.b.ReadBits64(12))
	for i := 0; syncword != 0xfff; i++ {
		if err := m.b.Err(); err != nil {
			return err
		}
		syncword <<= 8
		syncword &= 0xfff
		syncword |= uint16(m.b.ReadBits64(8))
		println("mis sync", i)
	}
	m.syncword = syncword
	m.ID = byte(m.b.ReadBits64(1))
	m.layer = Layer(m.b.ReadBits64(2))
	m.protection_bit = byte(m.b.ReadBits64(1))
	m.bitrate_index = byte(m.b.ReadBits64(4))
	m.sampling_frequency = byte(m.b.ReadBits64(2))
	m.padding_bit = byte(m.b.ReadBits64(1))
	m.private_bit = byte(m.b.ReadBits64(1))
	m.mode = Mode(m.b.ReadBits64(2))
	m.mode_extension = byte(m.b.ReadBits64(2))
	m.copyright = byte(m.b.ReadBits64(1))
	m.original_home = byte(m.b.ReadBits64(1))
	m.emphasis = Emphasis(m.b.ReadBits64(2))
	return m.b.Err()
}

func (m *MP3) error_check() {
	if m.protection_bit == 0 {
		m.b.ReadBits64(16)
	}
}

func (m *MP3) audio_data() []float32 {
	if m.mode == ModeSingle {
		main_data_end := uint16(m.b.ReadBits64(9))
		m.b.ReadBits64(5) // private_bits
		scfsi := make([]byte, cblimit)
		var part2_3_length [2]uint16
		var big_values [2]uint16
		var global_gain [2]uint16
		var scalefac_compress [2]byte
		var blocksplit_flag [2]byte
		var block_type [2]byte
		var switch_point [2]byte
		var table_select [3][2]byte
		var subblock_gain [3][2]uint8
		var region_address1, region_address2 [2]byte
		var preflag, scalefac_scale, count1table_select [2]byte
		var scalefac [][2]uint8
		var scalefacw [][3][2]uint8
		samples := make([][32]float64, 36)
		for scfsi_band := 0; scfsi_band < 4; scfsi_band++ {
			scfsi[scfsi_band] = byte(m.b.ReadBits64(1))
		}
		for gr := 0; gr < 2; gr++ {
			part2_3_length[gr] = uint16(m.b.ReadBits64(12))
			big_values[gr] = uint16(m.b.ReadBits64(9))
			global_gain[gr] = uint16(m.b.ReadBits64(8))
			scalefac_compress[gr] = byte(m.b.ReadBits64(4))
			blocksplit_flag[gr] = byte(m.b.ReadBits64(1))
			if blocksplit_flag[gr] != 0 {
				block_type[gr] = byte(m.b.ReadBits64(2))
				switch_point[gr] = byte(m.b.ReadBits64(1))
				for region := 0; region < 2; region++ {
					table_select[region][gr] = byte(m.b.ReadBits64(5))
				}
				for window := 0; window < 3; window++ {
					subblock_gain[window][gr] = uint8(m.b.ReadBits64(3))
				}
			} else {
				for region := 0; region < 3; region++ {
					table_select[region][gr] = byte(m.b.ReadBits64(5))
				}
				region_address1[gr] = byte(m.b.ReadBits64(4))
				region_address2[gr] = byte(m.b.ReadBits64(3))
			}
			preflag[gr] = byte(m.b.ReadBits64(1))
			scalefac_scale[gr] = byte(m.b.ReadBits64(1))
			count1table_select[gr] = byte(m.b.ReadBits64(1))
		}
		// The main_data follows. It does not follow the above side information in the bitstream. The main_data ends at a location in the main_data bitstream preceding the frame header of the following frame at an offset given by the value of main_data_end (see definition of main_data_end and 3-Annex Fig.3-A.7.1)
		for gr := 0; gr < 2; gr++ {
			if blocksplit_flag[gr] == 1 && block_type[gr] == 2 {
				scalefac = make([][2]uint8, switch_point_l(switch_point[gr]))
				scalefacw = make([][3][2]uint8, cblimit_short-switch_point_s(switch_point[gr]))
				for cb := 0; cb < switch_point_l(switch_point[gr]); cb++ {
					if (scfsi[cb] == 0) || (gr == 0) {
						slen := scalefactors_len(scalefac_compress[gr], block_type[gr], switch_point[gr], cb)
						scalefac[cb][gr] = uint8(m.b.ReadBits64(slen))
					}
				}
				for cb := switch_point_s(switch_point[gr]); cb < cblimit_short; cb++ {
					slen := scalefactors_len(scalefac_compress[gr], block_type[gr], switch_point[gr], cb)
					for window := 0; window < 3; window++ {
						if (scfsi[cb] == 0) || (gr == 0) {
							scalefacw[cb][window][gr] = uint8(m.b.ReadBits64(slen))
						}
					}
				}
			} else {
				scalefac = make([][2]uint8, cblimit)
				for cb := 0; cb < cblimit; cb++ {
					if (scfsi[cb] == 0) || (gr == 0) {
						slen := scalefactors_len(scalefac_compress[gr], block_type[gr], switch_point[gr], cb)
						scalefac[cb][gr] = uint8(m.b.ReadBits64(slen))
					}
				}
			}
			bits := uint(part2_3_length[gr]) - part2_length(switch_point[gr], scalefac_compress[gr], block_type[gr])
			region := 0
			entry := huffmanTables[table_select[region][gr]]
			isx := 0
			cb := 0
			rcount := region_address1[gr] + 1
			sfbwidthptr := 0
			sfbwidth := sfbwidthTable[m.sampling()].long
			if block_type[gr] == 2 {
				sfbwidth = sfbwidthTable[m.sampling()].short
			}
			sfbound := sfbwidth[sfbwidthptr]
			sfbwidthptr++
			var xr [576]float64
			var factor float64
			var sfm float64 = 2
			if scalefac_scale[gr] == 1 {
				sfm = 4
			}
			exp := func(i int) {
				cb += i
				if block_type[gr] == 2 {
					// todo: factor = ...
					panic("block type 2 - scale factors")
				} else {
					factor = (float64(global_gain[gr]) - 210) / 4
					factor -= sfm * (float64(scalefac[cb][gr]) + float64(preflag[gr]*pretab[cb]))
					factor = math.Pow(2, factor)
				}
			}
			exp(0)
			read := func(b byte) {
				d := int(b)
				if d == max_table_entry {
					// The spec says that the linbits values should be added to max_table_entry
					// - 1. libmad does not use a -1. I'm not sure if the spec is wrong, libmad
					// is wrong, or I'm misinterpreting the spec.
					d += int(m.b.ReadBits64(entry.linbits))
				}
				if d != 0 {
					xr[isx] = math.Pow(float64(d), 4.0/3.0) * factor
					if m.b.ReadBits64(1) == 1 {
						xr[isx] = -xr[isx]
					}
				}
				isx++
			}
			until := m.b.read + bits
			for big := big_values[gr]; big > 0 && m.b.read < until; big-- {
				if isx == sfbound {
					sfbound += sfbwidth[sfbwidthptr]
					sfbwidthptr++
					rcount--
					if rcount == 0 {
						if region == 0 {
							rcount = region_address1[gr] + 1
						} else {
							rcount = 0
						}
						region++
						entry = huffmanTables[table_select[region][gr]]
					}
					exp(1)
				}
				pair := entry.tree.Decode(m.b)
				read(pair[0])
				read(pair[1])
			}
			if m.b.read >= until {
				panic("huffman overrun")
			}
			table := huffmanQuadTables[count1table_select[gr]]
			setQuad := func(b, offset byte) {
				var v byte
				if b&offset != 0 {
					v = 1
				}
				read(v)
			}
			for m.b.read < until {
				if isx == sfbound {
					sfbound += sfbwidth[sfbwidthptr]
					sfbwidthptr++
					exp(1)
				}
				quad := table.Decode(m.b)[0]
				setQuad(quad, 1<<3) // v
				setQuad(quad, 1<<2) // w
				setQuad(quad, 1<<1) // x
				setQuad(quad, 1<<0) // y
			}
			/*
				for position != main_data_end {
					m.b.ReadBits64(1) // ancillary_bit
				}
			//*/
			// todo: determine channel blocktype, support blocktype == 2
			bt := block_type[0]
			ch := 0
			if bt != 2 {
				aliasReduce(xr[:])
				xi := make([]float64, 36)
				i := 0
				for sb := 0; sb < 32; sb++ {
					x := xr[i : i+18]
					i += 18
					imdct(x, xi)
					window(xi, bt)
					m.overlap(xi, samples[gr*18:], ch, sb)
					if sb&1 == 1 {
						freqinver(samples, sb)
					}
				}
			}
		}
		return m.synth(samples)
		_ = main_data_end
	}
	/* else if (mode == ModeStereo) || (mode == ModeDual) || (mode == ModeJoint) {
		main_data_end := uint16(m.b.ReadBits64(9))
		private_bits := byte(m.b.ReadBits64(3))
		for ch := 0; ch < 2; ch++ {
			for scfsi_band = 0; scfsi_band < 4; scfsi_band++ {
				scfsi[scfsi_band][ch] = byte(m.b.ReadBits64(1))
			}
		}
		for gr := 0; gr < 2; gr++ {
			for ch := 0; ch < 2; ch++ {
				part2_3_length[gr][ch] = uint16(m.b.ReadBits64(12))
				big_values[gr][ch] = uint16(m.b.ReadBits64(9))
				global_gain[gr][ch] = uint16(m.b.ReadBits64(8))
				scalefac_compress[gr][ch] = byte(m.b.ReadBits64(4))
				blocksplit_flag[gr][ch] = byte(m.b.ReadBits64(1))
				if blocksplit_flag[gr][ch] {
					block_type[gr][ch] = byte(m.b.ReadBits64(2))
					switch_point[gr][ch] = uint16(m.b.ReadBits64(1))
					for region := 0; region < 2; region++ {
						table_select[region][gr][ch] = byte(m.b.ReadBits64(5))
					}
					for window := 0; window < 3; window++ {
						subblock_gain[window][gr][ch] = uint8(m.b.ReadBits64(3))
					}
				} else {
					for region := 0; region < 3; region++ {
						table_select[region][gr][ch] = byte(m.b.ReadBits64(5))
					}
					region_address1[gr][ch] = byte(m.b.ReadBits64(4))
					region_address2[gr][ch] = byte(m.b.ReadBits64(3))
				}
				preflag[gr][ch] = byte(m.b.ReadBits64(1))
				scalefac_scale[gr][ch] = byte(m.b.ReadBits64(1))
				count1table_select[gr][ch] = byte(m.b.ReadBits64(1))
				// The main_data follows. It does not follow the above side information in the bitstream. The main_data endsat a location in the main_data bitstream preceding the frame header of the following frame at an offset given by thevalue of main_data_end.
			}
		}
		for gr := 0; gr < 2; gr++ {
			for ch := 0; ch < 2; ch++ {
				if blocksplit_flag[gr][ch] == 1 && block_type[gr][ch] == 2 {
					for cb := 0; cb < switch_point_l[gr][ch]; cb++ {
						if (scfsi[cb] == 0) || (gr == 0) {
							// scalefac[cb][gr][ch]0..4 bits uimsbf
						}
					}
					for cb := switch_point_s[gr][ch]; cb < cblimit_short; cb++ {
						for window := 0; window < 3; window++ {
							if (scfsi[cb] == 0) || (gr == 0) {
								// scalefac[cb][window][gr][ch] 0..4 bits uimsbf
							}
						}
					}
				} else {
					for cb := 0; cb < cblimit; cb++ {
						if (scfsi[cb] == 0) || (gr == 0) {
							// scalefac[cb][gr][ch]0..4 bits uimsbf
						}
					}
				}
				// Huffmancodebits (part2_3_length-part2_length) bits bslbf
				for position != main_data_end {
					ancillary_bit := byte(m.b.ReadBits64(1))
				}
			}
		}
	}
	//*/
	return nil
}

var (
	mp3CI = [8]float64{
		-0.6,
		-0.535,
		-0.33,
		-0.185,
		-0.095,
		-0.041,
		-0.0142,
		-0.0037,
	}
	mp3CS, mp3CA [8]float64
)

func init() {
	for i, v := range mp3CI {
		den := math.Sqrt(1 + math.Pow(v, 2))
		mp3CS[i] = 1 / den
		mp3CA[i] = v / den
	}
}

func aliasReduce(s []float64) {
	for x := 18; x < len(s); x += 18 {
		for i := 0; i < 8; i++ {
			a := s[x-i-1]
			b := s[x+i]
			s[x-i-1] = a*mp3CS[i] - b*mp3CA[i]
			s[x+i] = b*mp3CS[i] + a*mp3CA[i]
		}
	}
}

func imdct(in, out []float64) {
	n := len(out)
	fn := float64(n)
	fn2 := fn * 2
	fn_2 := fn / 2
	for i := 0; i < n; i++ {
		c := math.Pi / fn2 * (2*float64(i) + 1 + fn_2)
		var x float64
		for k := 0; k < n/2; k++ {
			x += in[k] * math.Cos(c*(2*float64(k)+1))
		}
		out[i] = x
	}
}

func (m *MP3) overlap(in []float64, samples [][32]float64, ch, sb int) {
	for i := 0; i < 18; i++ {
		samples[i][sb] = in[i] + m.ov[ch][sb][i]
		m.ov[ch][sb][i] = in[i+18]
	}
}

func freqinver(samples [][32]float64, sb int) {
	for i := 1; i < 18; i += 2 {
		samples[i][sb] = -samples[i][sb]
	}
}

var Nik [64][32]float64

func init() {
	for i := range Nik {
		for k := range Nik[i] {
			Nik[i][k] = math.Cos(float64(16+i) * float64(2*k+1) * math.Pi / 64)
		}
	}
}

func (m *MP3) synth(samples [][32]float64) []float32 {
	r := make([]float32, 32*len(samples))
	for i, v := range samples {
		offset := i * 32
		m.synth32(v[:], r[offset:offset+32])
	}
	return r
}

func (m *MP3) synth32(samples []float64, output []float32) {
	var U [512]float64
	var W [512]float32
	copy(m.V[64:], m.V[:])
	for i := 0; i < 64; i++ {
		m.V[i] = 0
		for k, Sk := range samples {
			m.V[i] += Nik[i][k] * Sk
		}
	}
	for i := 0; i < 7; i++ {
		for j := 0; j < 31; j++ {
			U[i*64+j] = m.V[i*128+j]
			U[i*64+32+j] = m.V[i*128+96+j]
		}
	}
	for i := 0; i < 512; i++ {
		W[i] = float32(U[i]) * mp3Di[i]
	}
	for j := 0; j < 32; j++ {
		var S float32
		for i := 0; i < 15; i++ {
			S += W[j+32*i]
		}
		output[j] = S
	}
}

func window(out []float64, bt byte) {
	for i := range out {
		fi := float64(i)
		var x float64
		switch bt {
		case 0:
			x = math.Sin(math.Pi / 36 * (fi + 0.5))
		case 1:
			switch {
			case i < 18:
				x = math.Sin(math.Pi / 36 * (fi + 0.5))
			case i < 24:
				x = 1
			case i < 30:
				x = math.Sin(math.Pi / 12 * (fi - 18 + 0.5))
			case i < 36:
				x = 0
			default:
				panic("unreachable")
			}
		case 3:
			switch {
			case i < 6:
				x = 0
			case i < 12:
				x = math.Sin(math.Pi / 12 * (fi - 6 + 0.5))
			case i < 18:
				x = 1
			case i < 36:
				x = math.Sin(math.Pi / 36 * (fi + 0.5))
			default:
				panic("unreachable")
			}
		default:
			panic("unsupported block type window")
		}
		out[i] *= x
	}
}

// length returns the frame length in bytes.
func (m *MP3) length() int {
	padding := 0
	if m.padding_bit != 0 {
		padding = 1
	}
	switch m.layer {
	case LayerI:
		return (12*m.bitrate()*1000/m.sampling() + padding) * 4
	case LayerII, LayerIII:
		return 144*m.bitrate()*1000/m.sampling() + padding
	default:
		return 0
	}
}

func (m *MP3) bitrate() int {
	switch {
	case m.layer == LayerIII:
		switch m.bitrate_index {
		case 1:
			return 32
		case 2:
			return 40
		case 3:
			return 48
		case 4:
			return 56
		case 5:
			return 64
		case 6:
			return 80
		case 7:
			return 96
		case 8:
			return 112
		case 9:
			return 128
		case 10:
			return 160
		case 11:
			return 192
		case 12:
			return 224
		case 13:
			return 256
		case 14:
			return 320
		}
	}
	return 0
}

func (m *MP3) sampling() int {
	switch m.sampling_frequency {
	case 0:
		return 44100
	case 1:
		return 48000
	case 2:
		return 32000
	}
	return 0
}

type Layer byte

const (
	LayerI   Layer = 3
	LayerII        = 2
	LayerIII       = 1
)

func (l Layer) String() string {
	switch l {
	case LayerI:
		return "layer I"
	case LayerII:
		return "layer II"
	case LayerIII:
		return "layer III"
	default:
		return "unknown"
	}
}

func (m *MP3) channels() int {
	switch m.mode {
	case ModeSingle:
		return 1
	case ModeStereo, ModeJoint, ModeDual:
		return 2
	}
	return 0
}

type Mode byte

const (
	ModeStereo Mode = 0
	ModeJoint       = 1
	ModeDual        = 2
	ModeSingle      = 3
)

type Emphasis byte

const (
	EmphasisNone  Emphasis = 0
	Emphasis50_15          = 1
	EmphasisCCIT           = 3
)

const (
	cblimit         = 21
	cblimit_short   = 12
	max_table_entry = 15
)

func switch_point_l(b byte) int {
	if b == 0 {
		return 0
	}
	return 8
}

func switch_point_s(b byte) int {
	if b == 0 {
		return 0
	}
	return 3
}

func part2_length(switch_point, scalefac_compress, block_type byte) uint {
	slen1, slen2 := slen12(scalefac_compress)
	switch switch_point {
	case 0:
		switch block_type {
		case 0, 1, 3:
			return 11*slen1 + 10*slen2
		case 2:
			return 18*slen1 + 18*slen2
		}
	case 1:
		switch block_type {
		case 0, 1, 3:
			return 11*slen1 + 10*slen2
		case 2:
			return 17*slen1 + 18*slen2
		}
	}
	panic("unreachable")
}

func slen12(scalefac_compress byte) (slen1, slen2 uint) {
	switch scalefac_compress {
	case 0:
		return 0, 0
	case 1:
		return 0, 1
	case 2:
		return 0, 2
	case 3:
		return 0, 3
	case 4:
		return 3, 0
	case 5:
		return 1, 1
	case 6:
		return 1, 2
	case 7:
		return 1, 3
	case 8:
		return 2, 1
	case 9:
		return 2, 2
	case 10:
		return 2, 3
	case 11:
		return 3, 1
	case 12:
		return 3, 2
	case 13:
		return 3, 3
	case 14:
		return 4, 2
	case 15:
		return 4, 3
	}
	panic("unreachable")
}

func scalefactors_len(scalefac_compress, block_type, switch_point byte, cb int) uint {
	slen1, slen2 := slen12(scalefac_compress)
	switch block_type {
	case 0, 1, 3:
		if cb <= 10 {
			return slen1
		}
		return slen2
	case 2:
		switch {
		case switch_point == 0 && cb <= 5:
			return slen1
		case switch_point == 0 && cb > 5:
			return slen2
		case switch_point == 1 && cb <= 5:
			// FIX: see spec note about long windows
			return slen1
		case switch_point == 1 && cb > 5:
			return slen2
		}
	}
	panic("unreachable")
}