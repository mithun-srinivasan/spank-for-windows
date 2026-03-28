package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"image/color"
	"io"
	"io/fs"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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

const (
	defaultMode        = "pain"
	defaultCooldownMs  = 750
	defaultSensitivity = 5
)

var allowedModes = map[string]struct{}{
	"pain": {},
	"sexy": {},
	"halo": {},
}

type cliConfig struct {
	mode        string
	minAmp      float64
	cooldownMs  int
	autoStart   bool
	headless    bool
	runDuration time.Duration
}

func clampSensitivity(v int) int {
	if v < 1 {
		return 1
	}
	if v > 10 {
		return 10
	}
	return v
}

func minAmpFromSensitivity(v int) float64 {
	v = clampSensitivity(v)
	return 0.16 - (float64(v)-1)*0.014
}

func sensitivityFromMinAmp(minAmp float64) int {
	v := int(math.Round((0.16-minAmp)/0.014)) + 1
	return clampSensitivity(v)
}

func sensitivityLabel(v int) string {
	labels := []string{"Very Low", "Low", "Medium-Low", "Medium", "Medium-High", "High", "Very High", "Extreme", "Max", "Ultra"}
	v = clampSensitivity(v)
	return labels[v-1]
}

func parseCLI() (cliConfig, error) {
	mode := flag.String("mode", defaultMode, "Audio mode: pain|sexy|halo")
	sensitivity := flag.Int("sensitivity", defaultSensitivity, "Sensitivity level (1-10)")
	minAmp := flag.Float64("min-amp", -1, "Minimum amplitude threshold (0.0-1.0), overrides -sensitivity")
	cooldown := flag.Int("cooldown-ms", defaultCooldownMs, "Cooldown between detected slaps in milliseconds")
	autoStart := flag.Bool("autostart", false, "Start listening immediately in GUI mode")
	headless := flag.Bool("headless", false, "Run without GUI and print detections to stdout")
	runFor := flag.Duration("run-for", 0, "Auto-stop duration in headless mode (example: 30s, 2m)")
	listModes := flag.Bool("list-modes", false, "Print available modes and exit")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags]\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintf(os.Stderr, "  %s -mode halo -autostart\n", filepath.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "  %s -headless -mode sexy -sensitivity 7 -run-for 1m\n\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}

	flag.Parse()

	if *listModes {
		fmt.Println("pain")
		fmt.Println("sexy")
		fmt.Println("halo")
		os.Exit(0)
	}

	if _, ok := allowedModes[*mode]; !ok {
		return cliConfig{}, fmt.Errorf("invalid mode %q: use pain, sexy, or halo", *mode)
	}
	if *cooldown <= 0 {
		return cliConfig{}, fmt.Errorf("-cooldown-ms must be greater than 0")
	}
	if *runFor < 0 {
		return cliConfig{}, fmt.Errorf("-run-for cannot be negative")
	}

	resolvedMinAmp := *minAmp
	if resolvedMinAmp < 0 {
		resolvedMinAmp = minAmpFromSensitivity(*sensitivity)
	}
	if resolvedMinAmp < 0 || resolvedMinAmp > 1 {
		return cliConfig{}, fmt.Errorf("-min-amp must be between 0.0 and 1.0")
	}

	return cliConfig{
		mode:        *mode,
		minAmp:      resolvedMinAmp,
		cooldownMs:  *cooldown,
		autoStart:   *autoStart,
		headless:    *headless,
		runDuration: *runFor,
	}, nil
}

func runHeadless(cfg cliConfig) {
	eng := &Engine{}
	eng.SetMinAmp(cfg.minAmp)
	eng.OnSlap = func(ts time.Time) {
		eng.mu.Lock()
		sc := eng.SlapCount
		eng.mu.Unlock()
		fmt.Printf("[%s] slap #%d\n", ts.Format(time.RFC3339), sc)
	}

	fmt.Printf("Starting headless mode: mode=%s min-amp=%.4f cooldown-ms=%d\n", cfg.mode, cfg.minAmp, cfg.cooldownMs)
	eng.Start(cfg.mode, cfg.minAmp, cfg.cooldownMs)
	defer eng.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if cfg.runDuration > 0 {
		fmt.Printf("Auto-stopping after %s\n", cfg.runDuration)
		timer := time.NewTimer(cfg.runDuration)
		defer timer.Stop()
		select {
		case <-sigCh:
			fmt.Println("Stopping due to signal")
		case <-timer.C:
			fmt.Println("Stopping after run duration")
		}
		return
	}

	fmt.Println("Press Ctrl+C to stop")
	<-sigCh
	fmt.Println("Stopping due to signal")
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
	rand.Seed(time.Now().UnixNano())

	cfg, err := parseCLI()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(2)
	}

	if cfg.headless {
		runHeadless(cfg)
		return
	}

	a := app.New()
	a.Settings().SetTheme(&spankTheme{})

	w := a.NewWindow("Spank")
	w.Resize(fyne.NewSize(900, 580))
	w.SetFixedSize(false)
	w.CenterOnScreen()

	eng := &Engine{}
	eng.SetMinAmp(cfg.minAmp)
	curMode := cfg.mode

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
				eng.Start(curMode, eng.GetMinAmp(), cfg.cooldownMs)
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
			eng.Start(curMode, eng.GetMinAmp(), cfg.cooldownMs)
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
	initialSensitivity := sensitivityFromMinAmp(cfg.minAmp)
	sensLabel := canvas.NewText("Sensitivity: "+sensitivityLabel(initialSensitivity), color.NRGBA{R: 119, G: 136, B: 170, A: 255})
	sensLabel.TextSize = 12
	sensSlider := widget.NewSlider(1, 10)
	sensSlider.Step = 1
	sensSlider.Value = float64(initialSensitivity)
	var sensTimer *time.Timer
	var sensMu sync.Mutex
	sensSlider.OnChanged = func(v float64) {
		s := clampSensitivity(int(math.Round(v)))
		sensLabel.Text = "Sensitivity: " + sensitivityLabel(s)
		sensLabel.Refresh()
		// Debounce engine sensitivity updates while user drags the slider.
		minAmp := minAmpFromSensitivity(s)
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
	if cfg.autoStart {
		startBtn.OnTapped()
	}
	w.ShowAndRun()
}
