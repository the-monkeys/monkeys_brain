package imgstrip

import (
	"encoding/binary"
	"io"
)

// unzig maps zig-zag index to natural 8x8 index (row-major).
var unzig = [64]int{
	0, 1, 8, 16, 9, 2, 3, 10,
	17, 24, 32, 25, 18, 11, 4, 5,
	12, 19, 26, 33, 40, 48, 41, 34,
	27, 20, 13, 6, 7, 14, 21, 28,
	35, 42, 49, 56, 57, 50, 43, 36,
	29, 22, 15, 23, 30, 37, 44, 51,
	58, 59, 52, 45, 38, 31, 39, 46,
	53, 60, 61, 54, 47, 55, 62, 63,
}

const maxJPEGCoeff = 32767

func coeffOK(v int32) bool {
	return v >= -maxJPEGCoeff && v <= maxJPEGCoeff
}

// losslessJPEGOrient applies a jpegtran-class MCU/DCT shuffle for EXIF
// orientations 2–8. The input must already have metadata APP segments dropped.
// On any structural mismatch or unsafe coefficient it returns an error.
func losslessJPEGOrient(data []byte, sof *jpegSOF, orient uint16) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
			err = fail("lossless jpeg panic: %v", r)
		}
	}()
	if sof == nil || orient < 2 || orient > 8 {
		return nil, fail("no lossless orient")
	}
	if !sof.mcuAligned() {
		return nil, fail("not mcu aligned")
	}
	fr, err := decodeJPEGCoeffs(data, sof)
	if err != nil {
		return nil, err
	}
	if err := transformJPEGCoeffs(fr, orient); err != nil {
		return nil, err
	}
	return encodeJPEGCoeffs(fr)
}

type jpegCompBlocks struct {
	id, h, v, tq uint8
	bx, by       int
	blocks       [][64]int32 // natural order
}

type jpegCoeffFrame struct {
	width, height int
	comps         []jpegCompBlocks
	dqt           [4][64]byte // zig-zag
	dqtOK         [4]bool
	app0          []byte
}

func decodeJPEGCoeffs(data []byte, sof *jpegSOF) (*jpegCoeffFrame, error) {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, fail("truncated jpeg")
	}
	fr := &jpegCoeffFrame{width: sof.width, height: sof.height}
	maxH, maxV := 1, 1
	fr.comps = make([]jpegCompBlocks, len(sof.comps))
	for i, c := range sof.comps {
		if int(c.h) > maxH {
			maxH = int(c.h)
		}
		if int(c.v) > maxV {
			maxV = int(c.v)
		}
		fr.comps[i] = jpegCompBlocks{id: c.id, h: c.h, v: c.v, tq: c.tq}
	}
	mcuW, mcuH := 8*maxH, 8*maxV
	nMCUX := fr.width / mcuW
	nMCUY := fr.height / mcuH
	for i := range fr.comps {
		c := &fr.comps[i]
		c.bx = nMCUX * int(c.h)
		c.by = nMCUY * int(c.v)
		c.blocks = make([][64]int32, c.bx*c.by)
	}

	var dhtDC, dhtAC [4]*huffDec
	var sosTd, sosTa []uint8
	var sosIdx []int
	var entropy []byte
	dri := 0

	i := 2
	for i < len(data) {
		if data[i] != 0xFF {
			return nil, fail("truncated jpeg")
		}
		j := i
		for j < len(data) && data[j] == 0xFF {
			j++
		}
		if j >= len(data) {
			return nil, fail("truncated jpeg")
		}
		marker := data[j]
		i = j + 1
		if marker == 0xD9 {
			break
		}
		if marker == 0xDA {
			if i+1 >= len(data) {
				return nil, fail("truncated jpeg")
			}
			n := int(binary.BigEndian.Uint16(data[i : i+2]))
			if n < 6 || i+n > len(data) {
				return nil, fail("truncated jpeg")
			}
			payload := data[i+2 : i+n]
			var err error
			sosIdx, sosTd, sosTa, err = parseSOS(payload, fr)
			if err != nil {
				return nil, err
			}
			entropy = data[i+n:]
			break
		}
		if (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			continue
		}
		if i+1 >= len(data) {
			return nil, fail("truncated jpeg")
		}
		n := int(binary.BigEndian.Uint16(data[i : i+2]))
		if n < 2 || i+n > len(data) {
			return nil, fail("truncated jpeg")
		}
		payload := data[i+2 : i+n]
		raw := data[j-1 : i+n]
		i += n
		switch marker {
		case 0xDB:
			if err := parseDQT(payload, fr); err != nil {
				return nil, err
			}
		case 0xC4:
			if err := parseDHT(payload, &dhtDC, &dhtAC); err != nil {
				return nil, err
			}
		case 0xDD:
			if len(payload) < 2 {
				return nil, fail("truncated jpeg")
			}
			dri = int(binary.BigEndian.Uint16(payload[:2]))
			if dri != 0 {
				return nil, fail("jpeg restart not supported")
			}
		case 0xE0:
			fr.app0 = append([]byte{}, raw...)
		}
	}
	if entropy == nil {
		return nil, fail("no jpeg scan")
	}
	if err := decodeEntropy(entropy, fr, nMCUX, nMCUY, sosIdx, sosTd, sosTa, dhtDC, dhtAC); err != nil {
		return nil, err
	}
	return fr, nil
}

func parseDQT(payload []byte, fr *jpegCoeffFrame) error {
	p := 0
	for p < len(payload) {
		if p >= len(payload) {
			return fail("bad dqt")
		}
		pq := payload[p] >> 4
		tq := payload[p] & 0x0f
		p++
		if tq > 3 || pq != 0 {
			return fail("unsupported dqt")
		}
		if p+64 > len(payload) {
			return fail("bad dqt")
		}
		copy(fr.dqt[tq][:], payload[p:p+64])
		fr.dqtOK[tq] = true
		p += 64
	}
	return nil
}

type huffDec struct {
	lut         [256]uint16
	minCodes    [16]int32
	maxCodes    [16]int32
	valsIndices [16]int32
	vals        [256]uint8
	nCodes      int32
}

func parseDHT(payload []byte, dc, ac *[4]*huffDec) error {
	p := 0
	for p < len(payload) {
		if p+17 > len(payload) {
			return fail("bad dht")
		}
		tc := payload[p] >> 4
		th := payload[p] & 0x0f
		p++
		if tc > 1 || th > 3 {
			return fail("bad dht")
		}
		h := &huffDec{}
		var nCodes [16]int32
		for i := 0; i < 16; i++ {
			nCodes[i] = int32(payload[p+i])
			h.nCodes += nCodes[i]
		}
		p += 16
		if h.nCodes <= 0 || h.nCodes > 256 || p+int(h.nCodes) > len(payload) {
			return fail("bad dht")
		}
		copy(h.vals[:h.nCodes], payload[p:p+int(h.nCodes)])
		p += int(h.nCodes)
		buildHuffLUT(h, nCodes)
		if tc == 0 {
			dc[th] = h
		} else {
			ac[th] = h
		}
	}
	return nil
}

func buildHuffLUT(h *huffDec, nCodes [16]int32) {
	var x, code uint32
	for i := uint32(0); i < 8; i++ {
		code <<= 1
		for j := int32(0); j < nCodes[i]; j++ {
			base := uint8(code << (7 - i))
			lutValue := uint16(h.vals[x])<<8 | uint16(2+i)
			for k := uint8(0); k < 1<<(7-i); k++ {
				h.lut[base|k] = lutValue
			}
			code++
			x++
		}
	}
	var c, index int32
	for i, n := range nCodes {
		if n == 0 {
			h.minCodes[i] = -1
			h.maxCodes[i] = -1
			h.valsIndices[i] = -1
		} else {
			h.minCodes[i] = c
			h.maxCodes[i] = c + n - 1
			h.valsIndices[i] = index
			c += n
			index += n
		}
		c <<= 1
	}
}

func parseSOS(payload []byte, fr *jpegCoeffFrame) ([]int, []uint8, []uint8, error) {
	if len(payload) < 4 {
		return nil, nil, nil, fail("bad sos")
	}
	ns := int(payload[0])
	if ns < 1 || len(payload) != 4+2*ns {
		return nil, nil, nil, fail("bad sos")
	}
	ss, se, ah := payload[1+2*ns], payload[2+2*ns], payload[3+2*ns]
	if ss != 0 || se != 63 || ah != 0 {
		return nil, nil, nil, fail("progressive jpeg")
	}
	idx := make([]int, ns)
	td := make([]uint8, ns)
	ta := make([]uint8, ns)
	for i := 0; i < ns; i++ {
		id := payload[1+2*i]
		sel := payload[2+2*i]
		found := -1
		for j, c := range fr.comps {
			if c.id == id {
				found = j
				break
			}
		}
		if found < 0 {
			return nil, nil, nil, fail("sos component")
		}
		idx[i] = found
		td[i] = sel >> 4
		ta[i] = sel & 0x0f
		if td[i] > 3 || ta[i] > 3 {
			return nil, nil, nil, fail("sos table")
		}
	}
	return idx, td, ta, nil
}

type bitReader struct {
	data []byte
	i    int
	a, n uint32
	m    uint32
}

func (r *bitReader) nextByte() (byte, error) {
	if r.i >= len(r.data) {
		return 0, io.ErrUnexpectedEOF
	}
	b := r.data[r.i]
	r.i++
	if b != 0xFF {
		return b, nil
	}
	if r.i >= len(r.data) {
		r.i--
		return 0, io.ErrUnexpectedEOF
	}
	n := r.data[r.i]
	r.i++
	if n == 0x00 {
		return 0xFF, nil
	}
	r.i -= 2
	return 0, io.ErrUnexpectedEOF
}

func (r *bitReader) ensure(need int) error {
	for int(r.n) < need {
		b, err := r.nextByte()
		if err != nil {
			return err
		}
		r.a = r.a<<8 | uint32(b)
		r.n += 8
		if r.m == 0 {
			r.m = 1 << 7
		} else {
			r.m <<= 8
		}
	}
	return nil
}

func (r *bitReader) decodeHuff(h *huffDec) (uint8, error) {
	if h == nil || h.nCodes == 0 {
		return 0, fail("missing huffman")
	}
	if r.n < 8 {
		if err := r.ensure(8); err != nil {
			return r.decodeHuffSlow(h)
		}
	}
	if v := h.lut[(r.a>>uint32(r.n-8))&0xff]; v != 0 {
		n := (v & 0xff) - 1
		r.n -= uint32(n)
		r.m >>= n
		return uint8(v >> 8), nil
	}
	return r.decodeHuffSlow(h)
}

func (r *bitReader) decodeHuffSlow(h *huffDec) (uint8, error) {
	var code int32
	for i := 0; i < 16; i++ {
		if r.n == 0 {
			if err := r.ensure(1); err != nil {
				return 0, err
			}
		}
		if r.a&r.m != 0 {
			code |= 1
		}
		r.n--
		r.m >>= 1
		if h.maxCodes[i] != -1 && code <= h.maxCodes[i] {
			return h.vals[h.valsIndices[i]+code-h.minCodes[i]], nil
		}
		code <<= 1
	}
	return 0, fail("bad huffman")
}

func (r *bitReader) receiveExtend(t uint8) (int32, error) {
	if t == 0 {
		return 0, nil
	}
	if err := r.ensure(int(t)); err != nil {
		return 0, err
	}
	r.n -= uint32(t)
	r.m >>= t
	s := int32(1) << t
	x := int32(r.a>>uint8(r.n)) & (s - 1)
	if x < s>>1 {
		x += ((-1) << t) + 1
	}
	return x, nil
}

func decodeEntropy(entropy []byte, fr *jpegCoeffFrame, nMCUX, nMCUY int, sosIdx []int, sosTd, sosTa []uint8, dcT, acT [4]*huffDec) error {
	r := &bitReader{data: entropy}
	var dcPred [4]int32
	for my := 0; my < nMCUY; my++ {
		for mx := 0; mx < nMCUX; mx++ {
			for si, ci := range sosIdx {
				c := &fr.comps[ci]
				hDC := dcT[sosTd[si]]
				hAC := acT[sosTa[si]]
				for vy := 0; vy < int(c.v); vy++ {
					for hx := 0; hx < int(c.h); hx++ {
						bx := mx*int(c.h) + hx
						by := my*int(c.v) + vy
						blk, pred, err := decodeBlock(r, hDC, hAC, dcPred[ci])
						if err != nil {
							return err
						}
						dcPred[ci] = pred
						fr.comps[ci].blocks[by*c.bx+bx] = blk
					}
				}
			}
		}
	}
	return nil
}

func decodeBlock(r *bitReader, hDC, hAC *huffDec, pred int32) ([64]int32, int32, error) {
	var b [64]int32
	cat, err := r.decodeHuff(hDC)
	if err != nil {
		return b, pred, err
	}
	if cat > 16 {
		return b, pred, fail("bad dc")
	}
	delta, err := r.receiveExtend(cat)
	if err != nil {
		return b, pred, err
	}
	pred += delta
	if !coeffOK(pred) {
		return b, pred, fail("jpeg coeff overflow")
	}
	b[0] = pred
	for zig := 1; zig < 64; {
		rs, err := r.decodeHuff(hAC)
		if err != nil {
			return b, pred, err
		}
		run, size := rs>>4, rs&0x0f
		if size != 0 {
			zig += int(run)
			if zig >= 64 {
				return b, pred, fail("bad ac")
			}
			ac, err := r.receiveExtend(size)
			if err != nil {
				return b, pred, err
			}
			if !coeffOK(ac) {
				return b, pred, fail("jpeg coeff overflow")
			}
			b[unzig[zig]] = ac
			zig++
			continue
		}
		if run == 0x0f {
			zig += 16
			continue
		}
		break
	}
	return b, pred, nil
}

func transformJPEGCoeffs(fr *jpegCoeffFrame, orient uint16) error {
	swapWH := orient == 5 || orient == 6 || orient == 7 || orient == 8
	for i := range fr.comps {
		c := &fr.comps[i]
		outBX, outBY := c.bx, c.by
		if swapWH {
			outBX, outBY = c.by, c.bx
		}
		out := make([][64]int32, outBX*outBY)
		for by := 0; by < c.by; by++ {
			for bx := 0; bx < c.bx; bx++ {
				src := c.blocks[by*c.bx+bx]
				nb, dx, dy := mapBlock(orient, src, bx, by, c.bx, c.by)
				out[dy*outBX+dx] = nb
			}
		}
		c.blocks = out
		c.bx, c.by = outBX, outBY
		if swapWH {
			c.h, c.v = c.v, c.h
		}
	}
	if swapWH {
		fr.width, fr.height = fr.height, fr.width
		for t := 0; t < 4; t++ {
			if fr.dqtOK[t] {
				fr.dqt[t] = transposeQ(fr.dqt[t])
			}
		}
	}
	return nil
}

func mapBlock(orient uint16, src [64]int32, bx, by, nBX, nBY int) (blk [64]int32, dx, dy int) {
	switch orient {
	case 2:
		return dctFlipH(src), nBX - 1 - bx, by
	case 3:
		return dctRot180(src), nBX - 1 - bx, nBY - 1 - by
	case 4:
		return dctFlipV(src), bx, nBY - 1 - by
	case 5:
		return dctTranspose(src), by, bx
	case 6:
		return dctRot90CW(src), nBY - 1 - by, bx
	case 7:
		return dctTransverse(src), nBY - 1 - by, nBX - 1 - bx
	case 8:
		return dctRot90CCW(src), by, nBX - 1 - bx
	default:
		return src, bx, by
	}
}

func dctFlipH(src [64]int32) [64]int32 {
	var d [64]int32
	for v := 0; v < 8; v++ {
		for u := 0; u < 8; u++ {
			s := src[v*8+u]
			if u&1 == 1 {
				s = -s
			}
			d[v*8+u] = s
		}
	}
	return d
}

func dctFlipV(src [64]int32) [64]int32 {
	var d [64]int32
	for v := 0; v < 8; v++ {
		for u := 0; u < 8; u++ {
			s := src[v*8+u]
			if v&1 == 1 {
				s = -s
			}
			d[v*8+u] = s
		}
	}
	return d
}

func dctRot180(src [64]int32) [64]int32 {
	return dctFlipH(dctFlipV(src))
}

func dctTranspose(src [64]int32) [64]int32 {
	var d [64]int32
	for v := 0; v < 8; v++ {
		for u := 0; u < 8; u++ {
			d[v*8+u] = src[u*8+v]
		}
	}
	return d
}

func dctRot90CW(src [64]int32) [64]int32 {
	var d [64]int32
	for j := 0; j < 8; j++ {
		for i := 0; i < 8; i++ {
			s := src[i*8+j]
			if i&1 == 1 {
				s = -s
			}
			d[j*8+i] = s
		}
	}
	return d
}

func dctRot90CCW(src [64]int32) [64]int32 {
	var d [64]int32
	for j := 0; j < 8; j++ {
		for i := 0; i < 8; i++ {
			s := src[i*8+j]
			if j&1 == 1 {
				s = -s
			}
			d[j*8+i] = s
		}
	}
	return d
}

func dctTransverse(src [64]int32) [64]int32 {
	var d [64]int32
	for j := 0; j < 8; j++ {
		for i := 0; i < 8; i++ {
			s := src[i*8+j]
			if (i+j)&1 == 1 {
				s = -s
			}
			d[j*8+i] = s
		}
	}
	return d
}

func transposeQ(zig [64]byte) [64]byte {
	var nat [64]byte
	for z := 0; z < 64; z++ {
		nat[unzig[z]] = zig[z]
	}
	var t [64]byte
	for v := 0; v < 8; v++ {
		for u := 0; u < 8; u++ {
			t[v*8+u] = nat[u*8+v]
		}
	}
	var out [64]byte
	for z := 0; z < 64; z++ {
		out[z] = t[unzig[z]]
	}
	return out
}

var bitCount = [256]byte{
	0, 1, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4,
	5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
}

type huffmanSpec struct {
	count [16]byte
	value []byte
}

var theHuffmanSpec = [4]huffmanSpec{
	{
		[16]byte{0, 1, 5, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0},
		[]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
	},
	{
		[16]byte{0, 2, 1, 3, 3, 2, 4, 3, 5, 5, 4, 4, 0, 0, 1, 125},
		[]byte{
			0x01, 0x02, 0x03, 0x00, 0x04, 0x11, 0x05, 0x12,
			0x21, 0x31, 0x41, 0x06, 0x13, 0x51, 0x61, 0x07,
			0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xa1, 0x08,
			0x23, 0x42, 0xb1, 0xc1, 0x15, 0x52, 0xd1, 0xf0,
			0x24, 0x33, 0x62, 0x72, 0x82, 0x09, 0x0a, 0x16,
			0x17, 0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28,
			0x29, 0x2a, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39,
			0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49,
			0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59,
			0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69,
			0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79,
			0x7a, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89,
			0x8a, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98,
			0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7,
			0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6,
			0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5,
			0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4,
			0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2,
			0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea,
			0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8,
			0xf9, 0xfa,
		},
	},
	{
		[16]byte{0, 3, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0},
		[]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
	},
	{
		[16]byte{0, 2, 1, 2, 4, 4, 3, 4, 7, 5, 4, 4, 0, 1, 2, 119},
		[]byte{
			0x00, 0x01, 0x02, 0x03, 0x11, 0x04, 0x05, 0x21,
			0x31, 0x06, 0x12, 0x41, 0x51, 0x07, 0x61, 0x71,
			0x13, 0x22, 0x32, 0x81, 0x08, 0x14, 0x42, 0x91,
			0xa1, 0xb1, 0xc1, 0x09, 0x23, 0x33, 0x52, 0xf0,
			0x15, 0x62, 0x72, 0xd1, 0x0a, 0x16, 0x24, 0x34,
			0xe1, 0x25, 0xf1, 0x17, 0x18, 0x19, 0x1a, 0x26,
			0x27, 0x28, 0x29, 0x2a, 0x35, 0x36, 0x37, 0x38,
			0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48,
			0x49, 0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58,
			0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68,
			0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78,
			0x79, 0x7a, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87,
			0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95, 0x96,
			0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5,
			0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4,
			0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3,
			0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2,
			0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda,
			0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9,
			0xea, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8,
			0xf9, 0xfa,
		},
	},
}

func init() {
	for i, s := range theHuffmanSpec {
		theHuffmanLUT[i].init(s)
	}
}

type huffmanLUT []uint32

var theHuffmanLUT [4]huffmanLUT

func (h *huffmanLUT) init(s huffmanSpec) {
	maxValue := 0
	for _, v := range s.value {
		if int(v) > maxValue {
			maxValue = int(v)
		}
	}
	*h = make([]uint32, maxValue+1)
	code, k := uint32(0), 0
	for i := 0; i < len(s.count); i++ {
		nBits := uint32(i+1) << 24
		for j := uint8(0); j < s.count[i]; j++ {
			(*h)[s.value[k]] = nBits | code
			code++
			k++
		}
		code <<= 1
	}
}

type bitWriter struct {
	buf         []byte
	bits, nBits uint32
}

func (w *bitWriter) emit(bits, nBits uint32) {
	nBits += w.nBits
	bits <<= 32 - nBits
	bits |= w.bits
	for nBits >= 8 {
		b := uint8(bits >> 24)
		w.buf = append(w.buf, b)
		if b == 0xff {
			w.buf = append(w.buf, 0x00)
		}
		bits <<= 8
		nBits -= 8
	}
	w.bits, w.nBits = bits, nBits
}

func (w *bitWriter) emitHuff(h int, value int32) error {
	if h < 0 || h >= len(theHuffmanLUT) {
		return fail("bad huffman table")
	}
	lut := theHuffmanLUT[h]
	if value < 0 || int(value) >= len(lut) {
		return fail("bad huffman code")
	}
	x := lut[value]
	if x == 0 && value != 0 {
		return fail("bad huffman code")
	}
	w.emit(x&(1<<24-1), x>>24)
	return nil
}

func (w *bitWriter) emitHuffRLE(h int, runLength, value int32) error {
	if runLength < 0 || runLength > 15 {
		return fail("bad jpeg rle run")
	}
	a, b := value, value
	if a < 0 {
		a, b = -value, value-1
	}
	if a >= 65536 {
		return fail("jpeg coeff overflow")
	}
	var nBits uint32
	if a < 0x100 {
		nBits = uint32(bitCount[a])
	} else {
		nBits = 8 + uint32(bitCount[a>>8])
	}
	if nBits > 16 {
		return fail("jpeg coeff overflow")
	}
	if err := w.emitHuff(h, runLength<<4|int32(nBits)); err != nil {
		return err
	}
	if nBits > 0 {
		w.emit(uint32(b)&(1<<nBits-1), nBits)
	}
	return nil
}

func (w *bitWriter) flushBits() {
	if w.nBits > 0 {
		w.emit(0x7F, 7)
	}
}

func encodeJPEGCoeffs(fr *jpegCoeffFrame) ([]byte, error) {
	out := []byte{0xFF, 0xD8}
	if len(fr.app0) > 0 {
		out = append(out, fr.app0...)
	}
	out = append(out, encodeDQT(fr)...)
	out = append(out, encodeSOF0(fr)...)
	out = append(out, encodeStdDHT(len(fr.comps))...)
	scan, err := encodeEntropy(fr)
	if err != nil {
		return nil, err
	}
	out = append(out, scan...)
	out = append(out, 0xFF, 0xD9)
	return out, nil
}

func encodeDQT(fr *jpegCoeffFrame) []byte {
	var body []byte
	for t := 0; t < 4; t++ {
		if !fr.dqtOK[t] {
			continue
		}
		body = append(body, byte(t))
		body = append(body, fr.dqt[t][:]...)
	}
	seg := make([]byte, 4+len(body))
	seg[0], seg[1] = 0xFF, 0xDB
	binary.BigEndian.PutUint16(seg[2:4], uint16(2+len(body)))
	copy(seg[4:], body)
	return seg
}

func encodeSOF0(fr *jpegCoeffFrame) []byte {
	n := len(fr.comps)
	body := make([]byte, 6+3*n)
	body[0] = 8
	binary.BigEndian.PutUint16(body[1:3], uint16(fr.height))
	binary.BigEndian.PutUint16(body[3:5], uint16(fr.width))
	body[5] = byte(n)
	for i, c := range fr.comps {
		body[6+3*i] = c.id
		body[7+3*i] = c.h<<4 | c.v
		body[8+3*i] = c.tq
	}
	seg := make([]byte, 4+len(body))
	seg[0], seg[1] = 0xFF, 0xC0
	binary.BigEndian.PutUint16(seg[2:4], uint16(2+len(body)))
	copy(seg[4:], body)
	return seg
}

func encodeStdDHT(nComp int) []byte {
	specs := theHuffmanSpec[:]
	if nComp == 1 {
		specs = specs[:2]
	}
	var body []byte
	for i, s := range specs {
		body = append(body, "\x00\x10\x01\x11"[i])
		body = append(body, s.count[:]...)
		body = append(body, s.value...)
	}
	seg := make([]byte, 4+len(body))
	seg[0], seg[1] = 0xFF, 0xC4
	binary.BigEndian.PutUint16(seg[2:4], uint16(2+len(body)))
	copy(seg[4:], body)
	return seg
}

func encodeEntropy(fr *jpegCoeffFrame) ([]byte, error) {
	n := len(fr.comps)
	sos := make([]byte, 4+1+2*n+3)
	sos[0], sos[1] = 0xFF, 0xDA
	binary.BigEndian.PutUint16(sos[2:4], uint16(2+1+2*n+3))
	sos[4] = byte(n)
	for i, c := range fr.comps {
		sos[5+2*i] = c.id
		if i == 0 {
			sos[6+2*i] = 0x00
		} else {
			sos[6+2*i] = 0x11
		}
	}
	off := 5 + 2*n
	sos[off] = 0
	sos[off+1] = 63
	sos[off+2] = 0

	maxH, maxV := 1, 1
	for _, c := range fr.comps {
		if int(c.h) > maxH {
			maxH = int(c.h)
		}
		if int(c.v) > maxV {
			maxV = int(c.v)
		}
	}
	nMCUX := fr.width / (8 * maxH)
	nMCUY := fr.height / (8 * maxV)
	w := &bitWriter{}
	var dcPred [4]int32
	for my := 0; my < nMCUY; my++ {
		for mx := 0; mx < nMCUX; mx++ {
			for ci := range fr.comps {
				c := &fr.comps[ci]
				hDC, hAC := 0, 1
				if ci != 0 {
					hDC, hAC = 2, 3
				}
				for vy := 0; vy < int(c.v); vy++ {
					for hx := 0; hx < int(c.h); hx++ {
						bx := mx*int(c.h) + hx
						by := my*int(c.v) + vy
						var err error
						dcPred[ci], err = encodeBlock(w, hDC, hAC, c.blocks[by*c.bx+bx], dcPred[ci])
						if err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}
	w.flushBits()
	return append(sos, w.buf...), nil
}

func encodeBlock(w *bitWriter, hDC, hAC int, b [64]int32, prevDC int32) (int32, error) {
	if !coeffOK(b[0]) {
		return prevDC, fail("jpeg coeff overflow")
	}
	dc := b[0]
	if err := w.emitHuffRLE(hDC, 0, dc-prevDC); err != nil {
		return prevDC, err
	}
	runLength := int32(0)
	for zig := 1; zig < 64; zig++ {
		ac := b[unzig[zig]]
		if ac == 0 {
			runLength++
			continue
		}
		if !coeffOK(ac) {
			return prevDC, fail("jpeg coeff overflow")
		}
		for runLength > 15 {
			if err := w.emitHuff(hAC, 0xf0); err != nil {
				return prevDC, err
			}
			runLength -= 16
		}
		if err := w.emitHuffRLE(hAC, runLength, ac); err != nil {
			return prevDC, err
		}
		runLength = 0
	}
	if runLength > 0 {
		if err := w.emitHuff(hAC, 0x00); err != nil {
			return prevDC, err
		}
	}
	return dc, nil
}
