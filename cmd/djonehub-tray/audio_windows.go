//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Call audio, like the macOS app's: the module's USB audio device ("AC
// Interface") carries the call at 8 kHz; the far end goes to this PC's
// speaker and this PC's microphone goes to the module.

const (
	sampleRate   = 8000
	frameSamples = 160 // 20 ms
	inBuffers    = 8
	outBuffers   = 12
	maxQueued    = 8 // 160 ms of playback backlog before dropping

	waveMapper          = 0xFFFFFFFF
	callbackEvent       = 0x00050000
	waveMapped          = 0x0004
	whdrDone            = 0x00000001
	drvQueryIface       = 0x080C // DRV_QUERYDEVICEINTERFACE
	drvQueryIfaceSize   = 0x080D
	drvmMapperPreferred = 0x2015 // DRVM_MAPPER_PREFERRED_GET
)

var (
	winmm              = windows.NewLazySystemDLL("winmm.dll")
	waveInGetNumDevs   = winmm.NewProc("waveInGetNumDevs")
	waveOutGetNumDevs  = winmm.NewProc("waveOutGetNumDevs")
	waveInMessage      = winmm.NewProc("waveInMessage")
	waveOutMessage     = winmm.NewProc("waveOutMessage")
	waveInOpen         = winmm.NewProc("waveInOpen")
	waveInClose        = winmm.NewProc("waveInClose")
	waveInPrepare      = winmm.NewProc("waveInPrepareHeader")
	waveInUnprepare    = winmm.NewProc("waveInUnprepareHeader")
	waveInAddBuffer    = winmm.NewProc("waveInAddBuffer")
	waveInStart        = winmm.NewProc("waveInStart")
	waveInReset        = winmm.NewProc("waveInReset")
	waveOutOpen        = winmm.NewProc("waveOutOpen")
	waveOutClose       = winmm.NewProc("waveOutClose")
	waveOutPrepare     = winmm.NewProc("waveOutPrepareHeader")
	waveOutUnprepare   = winmm.NewProc("waveOutUnprepareHeader")
	waveOutWrite       = winmm.NewProc("waveOutWrite")
	waveOutReset       = winmm.NewProc("waveOutReset")
	playSound          = winmm.NewProc("PlaySoundW")
	cfgmgr             = windows.NewLazySystemDLL("cfgmgr32.dll")
	cmLocateDevNode    = cfgmgr.NewProc("CM_Locate_DevNodeW")
	cmGetParent        = cfgmgr.NewProc("CM_Get_Parent")
	cmGetDeviceID      = cfgmgr.NewProc("CM_Get_Device_IDW")
	errNoModuleSpeaker = errors.New("没找到这个模块的音频设备（AC Interface）。请确认模块已开启通话支持")
)

type waveFormat struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

type waveHdr struct {
	Data          uintptr
	BufferLength  uint32
	BytesRecorded uint32
	User          uintptr
	Flags         uint32
	Loops         uint32
	Next          uintptr
	Reserved      uintptr
}

var pcmFormat = waveFormat{FormatTag: 1, Channels: 1, SamplesPerSec: sampleRate, AvgBytesPerSec: sampleRate * 2, BlockAlign: 2, BitsPerSample: 16}

// ---- devices ---------------------------------------------------------------

// devicePath is the PnP interface path of a wave device, for example
// \\?\usb#vid_2ca3&pid_4006&mi_06#7&3071712d&0&0006#{...}.
func devicePath(input bool, id uint32) string {
	msg := waveOutMessage
	if input {
		msg = waveInMessage
	}
	var size uint32
	if r, _, _ := msg.Call(uintptr(id), drvQueryIfaceSize, uintptr(unsafe.Pointer(&size)), 0); r != 0 || size == 0 {
		return ""
	}
	buf := make([]uint16, size/2+1)
	if r, _, _ := msg.Call(uintptr(id), drvQueryIface, uintptr(unsafe.Pointer(&buf[0])), uintptr(size)); r != 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}

// pathInstance turns an interface path back into its device instance ID.
func pathInstance(path string) string {
	p := strings.TrimPrefix(path, `\\?\`)
	parts := strings.Split(p, "#")
	if len(parts) < 3 {
		return ""
	}
	return strings.ToUpper(parts[0] + `\` + parts[1] + `\` + parts[2])
}

func parentInstance(instance string) string {
	var node, parent uint32
	if r, _, _ := cmLocateDevNode.Call(uintptr(unsafe.Pointer(&node)), uintptr(unsafe.Pointer(utf16(instance))), 0); r != 0 {
		return ""
	}
	if r, _, _ := cmGetParent.Call(uintptr(unsafe.Pointer(&parent)), uintptr(node), 0); r != 0 {
		return ""
	}
	buf := make([]uint16, 512)
	if r, _, _ := cmGetDeviceID.Call(uintptr(parent), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0); r != 0 {
		return ""
	}
	return strings.ToUpper(windows.UTF16ToString(buf))
}

// deviceModule is the composite USB device a wave device belongs to, or ""
// for audio that is not a USB function.
func deviceModule(input bool, id uint32) string {
	inst := pathInstance(devicePath(input, id))
	if !strings.HasPrefix(inst, `USB\`) {
		return ""
	}
	if strings.Contains(inst, "&MI_") {
		return parentInstance(inst)
	}
	return inst
}

func isModuleDevice(input bool, id uint32) bool {
	m := deviceModule(input, id)
	return strings.Contains(m, "VID_2CA3&") || strings.Contains(m, "VID_2C7C&")
}

func deviceCount(input bool) uint32 {
	p := waveOutGetNumDevs
	if input {
		p = waveInGetNumDevs
	}
	n, _, _ := p.Call()
	return uint32(n)
}

// findModuleDevice finds the module's own microphone or speaker.
func findModuleDevice(input bool, moduleInstance string) (uint32, bool) {
	want := strings.ToUpper(moduleInstance)
	for id := uint32(0); id < deviceCount(input); id++ {
		if deviceModule(input, id) == want {
			return id, true
		}
	}
	return 0, false
}

// pcDevice is the PC's preferred microphone or speaker, never a module's.
func pcDevice(input bool) (uint32, bool) {
	msg := waveOutMessage
	if input {
		msg = waveInMessage
	}
	var preferred, flags uint32 = waveMapper, 0
	msg.Call(waveMapper, drvmMapperPreferred, uintptr(unsafe.Pointer(&preferred)), uintptr(unsafe.Pointer(&flags)))
	if preferred != waveMapper && preferred < deviceCount(input) && !isModuleDevice(input, preferred) {
		return preferred, true
	}
	for id := uint32(0); id < deviceCount(input); id++ {
		if !isModuleDevice(input, id) {
			return id, true
		}
	}
	return 0, false
}

// ---- streams ---------------------------------------------------------------

type capture struct {
	h     uintptr
	event windows.Handle
	hdrs  []*waveHdr
	bufs  [][]byte
}

func openCapture(id uint32) (*capture, error) {
	ev, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil, err
	}
	c := &capture{event: ev}
	if r, _, _ := waveInOpen.Call(uintptr(unsafe.Pointer(&c.h)), uintptr(id), uintptr(unsafe.Pointer(&pcmFormat)), uintptr(ev), 0, callbackEvent|waveMapped); r != 0 {
		windows.CloseHandle(ev)
		return nil, fmt.Errorf("打开录音设备失败（MMRESULT %d）", r)
	}
	for i := 0; i < inBuffers; i++ {
		buf := make([]byte, frameSamples*2)
		h := &waveHdr{Data: uintptr(unsafe.Pointer(&buf[0])), BufferLength: uint32(len(buf))}
		waveInPrepare.Call(c.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h))
		waveInAddBuffer.Call(c.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h))
		c.hdrs, c.bufs = append(c.hdrs, h), append(c.bufs, buf)
	}
	waveInStart.Call(c.h)
	return c, nil
}

// next waits for recorded frames and hands each to fn.
func (c *capture) next(stop *atomic.Bool, fn func([]int16)) {
	samples := make([]int16, frameSamples)
	for !stop.Load() {
		windows.WaitForSingleObject(c.event, 100)
		for i, h := range c.hdrs {
			if h.Flags&whdrDone == 0 {
				continue
			}
			n := int(h.BytesRecorded) / 2
			for k := 0; k < n; k++ {
				samples[k] = int16(binary.LittleEndian.Uint16(c.bufs[i][k*2:]))
			}
			if n > 0 {
				fn(samples[:n])
			}
			h.Flags &^= whdrDone
			h.BytesRecorded = 0
			if !stop.Load() {
				waveInAddBuffer.Call(c.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h))
			}
		}
	}
}

func (c *capture) close() {
	waveInReset.Call(c.h)
	for _, h := range c.hdrs {
		waveInUnprepare.Call(c.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h))
	}
	waveInClose.Call(c.h)
	windows.CloseHandle(c.event)
}

type playback struct {
	mu   sync.Mutex
	h    uintptr
	hdrs []*waveHdr
	bufs [][]byte
	busy []bool
}

func openPlayback(id uint32) (*playback, error) {
	p := &playback{}
	if r, _, _ := waveOutOpen.Call(uintptr(unsafe.Pointer(&p.h)), uintptr(id), uintptr(unsafe.Pointer(&pcmFormat)), 0, 0, waveMapped); r != 0 {
		return nil, fmt.Errorf("打开播放设备失败（MMRESULT %d）", r)
	}
	for i := 0; i < outBuffers; i++ {
		buf := make([]byte, frameSamples*2)
		h := &waveHdr{Data: uintptr(unsafe.Pointer(&buf[0])), BufferLength: uint32(len(buf))}
		waveOutPrepare.Call(p.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h))
		h.Flags |= whdrDone
		p.hdrs, p.bufs, p.busy = append(p.hdrs, h), append(p.bufs, buf), append(p.busy, false)
	}
	return p, nil
}

// write queues one frame; a backlog beyond maxQueued is dropped so delay
// cannot grow when the two clocks drift apart.
func (p *playback) write(samples []int16) {
	p.mu.Lock()
	defer p.mu.Unlock()
	queued, free := 0, -1
	for i, h := range p.hdrs {
		if h.Flags&whdrDone == 0 {
			queued++
		} else if free < 0 {
			free = i
		}
	}
	if free < 0 || queued >= maxQueued {
		return
	}
	h, buf := p.hdrs[free], p.bufs[free]
	n := min(len(samples), frameSamples)
	for k := 0; k < n; k++ {
		binary.LittleEndian.PutUint16(buf[k*2:], uint16(samples[k]))
	}
	h.BufferLength = uint32(n * 2)
	h.Flags &^= whdrDone
	waveOutWrite.Call(p.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h))
}

func (p *playback) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	waveOutReset.Call(p.h)
	for _, h := range p.hdrs {
		waveOutUnprepare.Call(p.h, uintptr(unsafe.Pointer(h)), unsafe.Sizeof(*h))
	}
	waveOutClose.Call(p.h)
}

// ---- signal processing (as in the macOS app) -------------------------------

// biquad is one second-order section (direct form I).
type biquad struct{ b0, b1, b2, a1, a2, x1, x2, y1, y2 float64 }

func lowpass(fc, q float64) biquad {
	w := 2 * math.Pi * fc / sampleRate
	alpha := math.Sin(w) / (2 * q)
	cw := math.Cos(w)
	a0 := 1 + alpha
	return biquad{b0: (1 - cw) / 2 / a0, b1: (1 - cw) / a0, b2: (1 - cw) / 2 / a0, a1: -2 * cw / a0, a2: (1 - alpha) / a0}
}

func (f *biquad) run(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1, f.y2, f.y1 = f.x1, x, f.y1, y
	return y
}

// ---- bridge ----------------------------------------------------------------

type callBridge struct {
	stop        atomic.Bool
	muted       atomic.Bool
	farEnv      atomic.Uint64 // float64 bits
	wg          sync.WaitGroup
	streams     []func()
	recMu       sync.Mutex
	rec         *wavRecorder
	recFar      []int16
	recNear     []int16
	Description string
}

func startCallBridge(moduleInstance string) (*callBridge, error) {
	modIn, ok1 := findModuleDevice(true, moduleInstance)
	modOut, ok2 := findModuleDevice(false, moduleInstance)
	if !ok1 || !ok2 {
		return nil, errNoModuleSpeaker
	}
	pcIn, ok3 := pcDevice(true)
	pcOut, ok4 := pcDevice(false)
	if !ok4 {
		return nil, errors.New("这台电脑没有可用的扬声器或耳机")
	}
	if !ok3 {
		return nil, errors.New("这台电脑没有可用的麦克风")
	}
	b := &callBridge{}
	fail := func(err error) (*callBridge, error) { b.close(); return nil, err }

	speaker, err := openPlayback(pcOut)
	if err != nil {
		return fail(err)
	}
	b.streams = append(b.streams, speaker.close)
	toModule, err := openPlayback(modOut)
	if err != nil {
		return fail(err)
	}
	b.streams = append(b.streams, toModule.close)
	fromModule, err := openCapture(modIn)
	if err != nil {
		return fail(err)
	}
	b.streams = append(b.streams, fromModule.close)
	mic, err := openCapture(pcIn)
	if err != nil {
		return fail(err)
	}
	b.streams = append(b.streams, mic.close)

	// Far end -> PC speaker: 4th-order low-pass at 3 kHz removes the
	// module's 3.5-4 kHz noise; the envelope feeds echo suppression.
	b.wg.Add(2)
	go func() {
		defer b.wg.Done()
		runtime.LockOSThread()
		f1, f2 := lowpass(3000, 0.5412), lowpass(3000, 1.3066)
		env := 0.0
		out := make([]int16, frameSamples)
		fromModule.next(&b.stop, func(s []int16) {
			for i, v := range s {
				y := f2.run(f1.run(float64(v)))
				env = math.Max(math.Abs(y), env*0.995)
				out[i] = clamp16(y)
			}
			b.farEnv.Store(math.Float64bits(env))
			speaker.write(out[:len(s)])
			b.record(out[:len(s)], true)
		})
	}()
	// PC microphone -> far end: DC/hum high-pass, half-duplex echo
	// suppression while the far end talks, mute.
	go func() {
		defer b.wg.Done()
		runtime.LockOSThread()
		prevX, prevY := 0.0, 0.0
		const hp = 0.95
		gain := 1.0
		out := make([]int16, frameSamples)
		mic.next(&b.stop, func(s []int16) {
			far := math.Float64frombits(b.farEnv.Load())
			nearPeak := 0.0
			for _, v := range s {
				nearPeak = math.Max(nearPeak, math.Abs(float64(v)))
			}
			target := 1.0
			if far > 1200 && nearPeak < far*1.6 {
				target = 0.25
			}
			if b.muted.Load() {
				target = 0
			}
			for i, v := range s {
				x := float64(v)
				y := hp * (prevY + x - prevX)
				prevX, prevY = x, y
				gain += (target - gain) * 0.02
				out[i] = clamp16(y * gain)
			}
			toModule.write(out[:len(s)])
			b.record(out[:len(s)], false)
		})
	}()
	b.Description = fmt.Sprintf("module in %d / out %d, pc in %d / out %d", modIn, modOut, pcIn, pcOut)
	return b, nil
}

func clamp16(v float64) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func (b *callBridge) close() {
	if b == nil {
		return
	}
	b.stop.Store(true)
	b.wg.Wait()
	for i := len(b.streams) - 1; i >= 0; i-- {
		b.streams[i]()
	}
	b.streams = nil
	b.setRecording(false)
}

// ---- recording: stereo 8 kHz WAV, left = far end, right = this PC -----------

func recordingsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Documents", "DJOneHub", "通话录音")
}

type wavRecorder struct {
	f     *os.File
	bytes uint32
}

func (b *callBridge) setRecording(on bool) (string, error) {
	b.recMu.Lock()
	defer b.recMu.Unlock()
	if !on {
		if b.rec != nil {
			b.rec.finish()
			b.rec = nil
		}
		return "", nil
	}
	if b.rec != nil {
		return b.rec.f.Name(), nil
	}
	dir := recordingsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	f, err := os.Create(filepath.Join(dir, "通话录音_"+time.Now().Format("20060102_150405")+".wav"))
	if err != nil {
		return "", err
	}
	b.rec = &wavRecorder{f: f}
	b.rec.header()
	b.recFar, b.recNear = nil, nil
	return f.Name(), nil
}

func (b *callBridge) recording() bool {
	b.recMu.Lock()
	defer b.recMu.Unlock()
	return b.rec != nil
}

func (b *callBridge) record(s []int16, far bool) {
	b.recMu.Lock()
	defer b.recMu.Unlock()
	if b.rec == nil {
		return
	}
	if far {
		b.recFar = append(b.recFar, s...)
	} else {
		b.recNear = append(b.recNear, s...)
	}
	n := min(len(b.recFar), len(b.recNear))
	// A silent or stalled side must not hold the other one back for long.
	if n == 0 && max(len(b.recFar), len(b.recNear)) > sampleRate/2 {
		if len(b.recFar) == 0 {
			b.recFar = make([]int16, len(b.recNear))
		} else {
			b.recNear = make([]int16, len(b.recFar))
		}
		n = len(b.recFar)
	}
	if n == 0 {
		return
	}
	out := make([]byte, n*4)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(out[i*4:], uint16(b.recFar[i]))
		binary.LittleEndian.PutUint16(out[i*4+2:], uint16(b.recNear[i]))
	}
	b.rec.f.Write(out)
	b.rec.bytes += uint32(len(out))
	b.recFar, b.recNear = b.recFar[n:], b.recNear[n:]
}

func (r *wavRecorder) header() {
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 36+r.bytes)
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 2)
	binary.LittleEndian.PutUint32(h[24:], sampleRate)
	binary.LittleEndian.PutUint32(h[28:], sampleRate*4)
	binary.LittleEndian.PutUint16(h[32:], 4)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], r.bytes)
	r.f.WriteAt(h, 0)
}

func (r *wavRecorder) finish() {
	r.header()
	r.f.Close()
}

// ---- ringtone --------------------------------------------------------------

const (
	sndAsync     = 0x0001
	sndNoDefault = 0x0002
	sndLoop      = 0x0008
	sndFilename  = 0x00020000
)

var ringing atomic.Bool

func startRingtone(name string) {
	if name == "off" || ringing.Load() {
		return
	}
	if name == "" || strings.ContainsAny(name, `\/.`) {
		name = "Ring01"
	}
	path := filepath.Join(os.Getenv("WINDIR"), "Media", name+".wav")
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(os.Getenv("WINDIR"), "Media", "Ring01.wav")
	}
	ringing.Store(true)
	playSound.Call(uintptr(unsafe.Pointer(utf16(path))), 0, sndFilename|sndAsync|sndLoop|sndNoDefault)
}

func stopRingtone() {
	if ringing.Swap(false) {
		playSound.Call(0, 0, 0)
	}
}
