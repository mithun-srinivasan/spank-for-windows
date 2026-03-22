package main

import (
	"bytes"
	"embed"
	"fmt"
	"image/color"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"

	"github.com/mithun-srinivasan/spank-for-windows/detector"
	"github.com/mithun-srinivasan/spank-for-windows/sensor"
)

// ── Embedded assets ───────────────────────────────────────────────────────

//go:embed audio/pain/*.mp3
var painFS embed.FS

//go:embed audio/sexy/*.mp3
var sexyFS embed.FS

//go:embed audio/halo/*.mp3
var haloFS embed.FS

//go:embed logo.jpg
var logoData []byte

// ── Audio ─────────────────────────────────────────────────────────────────

var (
	spkOnce   sync.Once
	spkFormat beep.Format
	spkMu     sync.Mutex
)

func listEmbed(efs embed.FS, dir string) []string {
	var out []string
	entries, _ := fs.ReadDir(efs, dir)
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, dir+"/"+e.Name())
		}
	}
	return out
}

func playEmbed(efs embed.FS, path string) {
	data, err := efs.ReadFile(strings.ReplaceAll(path, "\\", "/"))
	if err != nil {
		return
	}
	playBytes(data)
}

func playBytes(data []byte) {
	st, fmt2, err := mp3.Decode(io.NopCloser(bytes.NewReader(data)))
	if err != nil {
		return
	}
	spkOnce.Do(func() {
		spkFormat = fmt2
		speaker.Init(fmt2.SampleRate, fmt2.SampleRate.N(time.Second/10))
	})
	rs := beep.Resample(4, fmt2.SampleRate, spkFormat.SampleRate, st)
	done := make(chan struct{})
	spkMu.Lock()
	speaker.Play(beep.Seq(rs, beep.Callback(func() { st.Close(); close(done) })))
	spkMu.Unlock()
	<-done
}

func pick(files []string) string {
	if len(files) == 0 {
		return ""
	}
	return files[rand.Intn(len(files))]
}

// ── Slap counter ──────────────────────────────────────────────────────────

type slapCounter struct {
	mu  sync.Mutex
	win []time.Time
	dur time.Duration
}

func newCounter(d time.Duration) *slapCounter { return &slapCounter{dur: d} }

func (c *slapCounter) add(t time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	cut := t.Add(-c.dur)
	fresh := c.win[:0]
	for _, ts := range c.win {
		if ts.After(cut) {
			fresh = append(fresh, ts)
		}
	}
	c.win = append(fresh, t)
	return len(c.win)
}

// ── Engine ────────────────────────────────────────────────────────────────

type Engine struct {
	mu        sync.Mutex
	running   bool
	stopCh    chan struct{}
	SlapCount int
	OnSlap    func(time.Time)
	minAmpVal float64
}

func (e *Engine) SetMinAmp(v float64) {
	e.mu.Lock()
	e.minAmpVal = v
	e.mu.Unlock()
}

func (e *Engine) GetMinAmp() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.minAmpVal == 0 {
		return 0.05
	}
	return e.minAmpVal
}

func (e *Engine) Start(mode string, minAmp float64, cooldownMs int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return
	}
	e.running = true
	e.stopCh = make(chan struct{})
	go e.loop(mode, minAmp, time.Duration(cooldownMs)*time.Millisecond, e.stopCh)
}

func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return
	}
	e.running = false
	close(e.stopCh)
}

func (e *Engine) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

func (e *Engine) loop(mode string, minAmp float64, cooldown time.Duration, stop chan struct{}) {
	r, _ := sensor.NewReader(5 * time.Millisecond)
	r.Start()
	defer r.Close()

	cfg := detector.DefaultConfig()
	cfg.MinAmplitude = minAmp
	det := detector.NewDetector(cfg)
	lastMinAmp := minAmp

	pain := listEmbed(painFS, "audio/pain")
	sexy := listEmbed(sexyFS, "audio/sexy")
	halo := listEmbed(haloFS, "audio/halo")
	cnt := newCounter(5 * time.Minute)

	var lastHit time.Time
	var hitMu sync.Mutex

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// Live sensitivity update — no restart needed
			newAmp := e.GetMinAmp()
			if newAmp != lastMinAmp {
				lastMinAmp = newAmp
				cfg.MinAmplitude = newAmp
				det = detector.NewDetector(cfg)
			}
			for _, s := range r.Drain() {
				if !det.AddSample(detector.Sample{Amplitude: s.Amplitude, T: s.T}) {
					continue
				}
				now := time.Now()
				hitMu.Lock()
				if now.Sub(lastHit) < cooldown {
					hitMu.Unlock()
					continue
				}
				lastHit = now
				hitMu.Unlock()

				e.mu.Lock()
				e.SlapCount++
				e.mu.Unlock()

				if e.OnSlap != nil {
					e.OnSlap(now)
				}

				go func(ts time.Time) {
					switch mode {
					case "pain":
						if f := pick(pain); f != "" {
							playEmbed(painFS, f)
						}
					case "sexy":
						n := cnt.add(ts)
						lvl := n - 1
						if lvl < 0 {
							lvl = 0
						}
						if lvl >= len(sexy) {
							lvl = len(sexy) - 1
						}
						playEmbed(sexyFS, sexy[lvl])
					case "halo":
						if f := pick(halo); f != "" {
							playEmbed(haloFS, f)
						}
					}
				}(now)
			}
		}
	}
}

// ── Custom dark theme ─────────────────────────────────────────────────────

type spankTheme struct{}

func (spankTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 13, G: 13, B: 26, A: 255}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 240, G: 240, B: 240, A: 255}
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 233, G: 69, B: 96, A: 255}
	case theme.ColorNameButton:
		return color.NRGBA{R: 22, G: 33, B: 62, A: 255}
	case theme.ColorNameDisabledButton:
		return color.NRGBA{R: 30, G: 40, B: 70, A: 255}
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 18, G: 18, B: 42, A: 255}
	case theme.ColorNameShadow:
		return color.NRGBA{R: 0, G: 0, B: 0, A: 80}
	case theme.ColorNameHover:
		return color.NRGBA{R: 233, G: 69, B: 96, A: 40}
	}
	return theme.DefaultTheme().Color(n, v)
}

func (spankTheme) Font(s fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(s)
}

func (spankTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(n)
}

func (spankTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 14
	case theme.SizeNamePadding:
		return 10
	case theme.SizeNameInnerPadding:
		return 8
	}
	return theme.DefaultTheme().Size(n)
}

// ── GUI ───────────────────────────────────────────────────────────────────

func main() {
	a := app.New()
	a.Settings().SetTheme(&spankTheme{})

	w := a.NewWindow("Spank")
	w.Resize(fyne.NewSize(900, 580))
	w.SetFixedSize(false)
	w.CenterOnScreen()

	eng := &Engine{}
	curMode := "pain"

	// ── Logo ──────────────────────────────────────────────────────────────
	logoRes := fyne.NewStaticResource("logo.jpg", logoData)
	logoImg := canvas.NewImageFromResource(logoRes)
	logoImg.FillMode = canvas.ImageFillContain
	logoImg.SetMinSize(fyne.NewSize(280, 120))

	// ── Slap counter ──────────────────────────────────────────────────────
	slapNum := canvas.NewText("0", color.NRGBA{R: 233, G: 69, B: 96, A: 255})
	slapNum.TextSize = 72
	slapNum.Alignment = fyne.TextAlignCenter
	slapNum.TextStyle = fyne.TextStyle{Bold: true}

	slapLabel := canvas.NewText("SLAPS DETECTED", color.NRGBA{R: 119, G: 136, B: 170, A: 255})
	slapLabel.TextSize = 12
	slapLabel.Alignment = fyne.TextAlignCenter

	// ── Status label ──────────────────────────────────────────────────────
	statusLabel := canvas.NewText("Ready — click START", color.NRGBA{R: 119, G: 136, B: 170, A: 255})
	statusLabel.TextSize = 12
	statusLabel.Alignment = fyne.TextAlignCenter

	// ── Status pill ───────────────────────────────────────────────────────
	statusPill := widget.NewLabel("● STOPPED")
	statusPill.Alignment = fyne.TextAlignCenter

	// ── Mode buttons ──────────────────────────────────────────────────────
	var modeButtons []*widget.Button
	modes := []struct {
		id, emoji, label, desc string
	}{
		{"pain", "😣", "PAIN", "Ow! Stop it!"},
		{"sexy", "🔥", "SEXY", "Getting spicy..."},
		{"halo", "🎮", "HALO", "Halo death sound"},
	}

	updateModeButtons := func() {
		for i, m := range modes {
			if m.id == curMode {
				modeButtons[i].Importance = widget.HighImportance
			} else {
				modeButtons[i].Importance = widget.LowImportance
			}
			modeButtons[i].Refresh()
		}
	}

	for _, m := range modes {
		m := m
		btn := widget.NewButton(m.emoji+" "+m.label+"\n"+m.desc, func() {
			wasRunning := eng.IsRunning()
			if wasRunning {
				eng.Stop()
			}
			curMode = m.id
			updateModeButtons()
			if wasRunning {
				eng.SetMinAmp(0.05)
				eng.Start(curMode, 0.05, 750)
			}
			statusLabel.Text = "Mode: " + m.label
			statusLabel.Refresh()
		})
		btn.Importance = widget.LowImportance
		modeButtons = append(modeButtons, btn)
	}
	updateModeButtons()

	modeRow := container.NewGridWithColumns(3,
		modeButtons[0], modeButtons[1], modeButtons[2],
	)

	// ── Main toggle button ────────────────────────────────────────────────
	startBtn := widget.NewButton("▶  START", nil)
	startBtn.Importance = widget.HighImportance

	startBtn.OnTapped = func() {
		if eng.IsRunning() {
			eng.Stop()
			startBtn.SetText("▶  START")
			startBtn.Importance = widget.HighImportance
			statusPill.SetText("● STOPPED")
			statusLabel.Text = "Stopped"
			statusLabel.Color = color.NRGBA{R: 119, G: 136, B: 170, A: 255}
		} else {
			eng.OnSlap = func(ts time.Time) {
				eng.mu.Lock()
				sc := eng.SlapCount
				eng.mu.Unlock()
				slapNum.Text = fmt.Sprintf("%d", sc)
				slapNum.Refresh()
				statusLabel.Text = fmt.Sprintf("*SLAP* at %s", ts.Format("15:04:05"))
				statusLabel.Color = color.NRGBA{R: 233, G: 69, B: 96, A: 255}
				statusLabel.Refresh()
			}
			eng.SetMinAmp(0.05)
				eng.Start(curMode, 0.05, 750)
			startBtn.SetText("■  STOP")
			startBtn.Importance = widget.DangerImportance
			statusPill.SetText("● ACTIVE")
			statusLabel.Text = "Listening for slaps..."
			statusLabel.Color = color.NRGBA{R: 15, G: 191, B: 106, A: 255}
		}
		statusLabel.Refresh()
		startBtn.Refresh()
	}

	// ── Sensitivity slider ────────────────────────────────────────────────
	sensLabel := canvas.NewText("Sensitivity: Medium", color.NRGBA{R: 119, G: 136, B: 170, A: 255})
	sensLabel.TextSize = 12
	sensSlider := widget.NewSlider(1, 10)
	sensSlider.Value = 5
	var sensTimer *time.Timer
	var sensMu sync.Mutex
	sensSlider.OnChanged = func(v float64) {
		labels := []string{"Very Low", "Low", "Medium-Low", "Medium", "Medium-High", "High", "Very High", "Extreme", "Max", "Ultra"}
		idx := int(v) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(labels) {
			idx = len(labels) - 1
		}
		sensLabel.Text = "Sensitivity: " + labels[idx]
		sensLabel.Refresh()
		// Debounce: only restart engine 500ms after user stops dragging
		minAmp := 0.16 - (v-1)*0.014
		sensMu.Lock()
		if sensTimer != nil {
			sensTimer.Stop()
		}
		sensTimer = time.AfterFunc(300*time.Millisecond, func() {
			eng.SetMinAmp(minAmp)
		})
		sensMu.Unlock()
	}

	// ── Uninstall ─────────────────────────────────────────────────────────
	uninstallBtn := widget.NewButton("Uninstall", func() {
		dialog.ShowConfirm("Uninstall Spank",
			"Remove Spank completely?\nThis deletes the app, shortcuts, and all files.",
			func(ok bool) {
				if !ok {
					return
				}
				eng.Stop()
				script := "@echo off\r\ntimeout /t 2 /nobreak >nul\r\n" +
					"rmdir /s /q \"%LOCALAPPDATA%\\Spank\" 2>nul\r\n" +
					"del /f /q \"%USERPROFILE%\\Desktop\\Spank.lnk\" 2>nul\r\n" +
					"del /f /q \"%APPDATA%\\Microsoft\\Windows\\Start Menu\\Programs\\Spank.lnk\" 2>nul\r\n"
				tmp := filepath.Join(os.TempDir(), "spank_uninstall.bat")
				_ = os.WriteFile(tmp, []byte(script), 0644)
				cmd := exec.Command("cmd.exe", "/c", tmp)

				_ = cmd.Start()
				a.Quit()
			}, w)
	})
	uninstallBtn.Importance = widget.LowImportance

	// ── Layout ────────────────────────────────────────────────────────────

	// Left panel
	leftBg := canvas.NewRectangle(color.NRGBA{R: 18, G: 18, B: 42, A: 255})

	leftContent := container.NewVBox(
		container.NewPadded(logoImg),
		widget.NewSeparator(),
		container.NewCenter(slapNum),
		container.NewCenter(slapLabel),
		widget.NewSeparator(),
		container.NewCenter(statusLabel),
		container.NewCenter(uninstallBtn),
	)
	leftPanel := container.NewStack(leftBg, container.NewPadded(leftContent))

	// Right panel
	titleText := canvas.NewText("SPANK", color.NRGBA{R: 233, G: 69, B: 96, A: 255})
	titleText.TextSize = 32
	titleText.TextStyle = fyne.TextStyle{Bold: true}
	titleText.Alignment = fyne.TextAlignCenter

	tagText := canvas.NewText("Slap your laptop, it yells back", color.NRGBA{R: 119, G: 136, B: 170, A: 255})
	tagText.TextSize = 13
	tagText.Alignment = fyne.TextAlignCenter

	modeTitle := canvas.NewText("SELECT MODE", color.NRGBA{R: 119, G: 136, B: 170, A: 255})
	modeTitle.TextSize = 11
	modeTitle.Alignment = fyne.TextAlignCenter

	sensTitle := canvas.NewText("SENSITIVITY", color.NRGBA{R: 119, G: 136, B: 170, A: 255})
	sensTitle.TextSize = 11
	sensTitle.Alignment = fyne.TextAlignCenter

	rightContent := container.NewVBox(
		container.NewCenter(titleText),
		container.NewCenter(tagText),
		widget.NewSeparator(),
		container.NewCenter(statusPill),
		container.NewPadded(startBtn),
		widget.NewSeparator(),
		container.NewCenter(modeTitle),
		container.NewPadded(modeRow),
		widget.NewSeparator(),
		container.NewCenter(sensTitle),
		container.NewPadded(sensLabel),
		container.NewPadded(sensSlider),
	)
	rightPanel := container.NewPadded(rightContent)

	// Split layout
	split := container.NewHSplit(leftPanel, rightPanel)
	split.SetOffset(0.35)

	w.SetContent(split)
	w.ShowAndRun()
}
