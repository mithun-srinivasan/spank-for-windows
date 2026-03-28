//go:build windows

package sensor

import (
	"fmt"
	"math"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type Sample struct {
	Amplitude float64
	T         time.Time
}

type Reader struct {
	mu       sync.Mutex
	samples  []Sample
	stop     chan struct{}
	interval time.Duration
}

var (
	ole32    = syscall.NewLazyDLL("ole32.dll")
	coInit   = ole32.NewProc("CoInitializeEx")
	coCreate = ole32.NewProc("CoCreateInstance")
	coUninit = ole32.NewProc("CoUninitialize")
)

const ptrSz = unsafe.Sizeof(uintptr(0))

func vtbl(obj uintptr, slot uintptr) uintptr {
	return *(*uintptr)(unsafe.Pointer(*(*uintptr)(unsafe.Pointer(obj)) + slot*ptrSz))
}

func comRelease(obj uintptr) {
	if obj != 0 {
		syscall.Syscall(vtbl(obj, 2), 1, obj, 0, 0)
	}
}

var (
	clsidMMDE = syscall.GUID{Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C, Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidMMDE   = syscall.GUID{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35, Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidAC     = syscall.GUID{Data1: 0x1CB9AD4C, Data2: 0xDBFA, Data3: 0x4c32, Data4: [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidACC    = syscall.GUID{Data1: 0xC8ADBD64, Data2: 0xE71E, Data3: 0x48a0, Data4: [8]byte{0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17}}
)

func NewReader(interval time.Duration) (*Reader, error) {
	return &Reader{stop: make(chan struct{}), interval: interval}, nil
}

func (r *Reader) Start() { go r.loop() }

func (r *Reader) loop() {
	coInit.Call(0, 0)
	defer coUninit.Call()
	var en uintptr
	if hr, _, _ := coCreate.Call(uintptr(unsafe.Pointer(&clsidMMDE)), 0, 0x17, uintptr(unsafe.Pointer(&iidMMDE)), uintptr(unsafe.Pointer(&en))); hr != 0 || en == 0 {
		fmt.Fprintf(os.Stderr, "WASAPI enum failed 0x%08X\n", hr)
		return
	}
	defer comRelease(en)
	var dev uintptr
	if hr, _, _ := syscall.Syscall6(vtbl(en, 4), 4, en, 1, 0, uintptr(unsafe.Pointer(&dev)), 0, 0); hr != 0 || dev == 0 {
		fmt.Fprintf(os.Stderr, "WASAPI no device 0x%08X\n", hr)
		return
	}
	defer comRelease(dev)
	var ac uintptr
	if hr, _, _ := syscall.Syscall6(vtbl(dev, 3), 5, dev, uintptr(unsafe.Pointer(&iidAC)), 0x17, 0, uintptr(unsafe.Pointer(&ac)), 0); hr != 0 || ac == 0 {
		return
	}
	defer comRelease(ac)
	var fmtPtr uintptr
	syscall.Syscall(vtbl(ac, 8), 2, ac, uintptr(unsafe.Pointer(&fmtPtr)), 0)
	if fmtPtr == 0 {
		return
	}
	if hr, _, _ := syscall.Syscall6(vtbl(ac, 3), 6, ac, 0, 0, 100*10000, 0, fmtPtr); hr != 0 {
		ole32.NewProc("CoTaskMemFree").Call(fmtPtr)
		return
	}
	ole32.NewProc("CoTaskMemFree").Call(fmtPtr)
	var cc uintptr
	if hr, _, _ := syscall.Syscall(vtbl(ac, 14), 3, ac, uintptr(unsafe.Pointer(&iidACC)), uintptr(unsafe.Pointer(&cc))); hr != 0 || cc == 0 {
		return
	}
	defer comRelease(cc)
	syscall.Syscall(vtbl(ac, 10), 1, ac, 0, 0)
	defer syscall.Syscall(vtbl(ac, 11), 1, ac, 0, 0)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case t := <-ticker.C:
			amp := peak(cc)
			r.mu.Lock()
			r.samples = append(r.samples, Sample{Amplitude: amp, T: t})
			if len(r.samples) > 8192 {
				r.samples = r.samples[len(r.samples)-8192:]
			}
			r.mu.Unlock()
		}
	}
}

func peak(cc uintptr) float64 {
	p := 0.0
	for {
		var nf uint32
		if hr, _, _ := syscall.Syscall(vtbl(cc, 5), 2, cc, uintptr(unsafe.Pointer(&nf)), 0); hr != 0 || nf == 0 {
			break
		}
		var dp uintptr
		var frames, flags uint32
		var d1, d2 uint64
		if hr, _, _ := syscall.Syscall6(vtbl(cc, 3), 6, cc, uintptr(unsafe.Pointer(&dp)), uintptr(unsafe.Pointer(&frames)), uintptr(unsafe.Pointer(&flags)), uintptr(unsafe.Pointer(&d1)), uintptr(unsafe.Pointer(&d2))); hr != 0 || dp == 0 || frames == 0 {
			break
		}
		if flags&2 == 0 {
			buf := unsafe.Slice((*float32)(unsafe.Pointer(dp)), int(frames)*2)
			for _, s := range buf {
				if v := math.Abs(float64(s)); v > p {
					p = v
				}
			}
		}
		syscall.Syscall(vtbl(cc, 4), 2, cc, uintptr(frames), 0)
	}
	return p
}

func (r *Reader) Drain() []Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Sample, len(r.samples))
	copy(out, r.samples)
	r.samples = r.samples[:0]
	return out
}

func (r *Reader) Close() { close(r.stop) }
