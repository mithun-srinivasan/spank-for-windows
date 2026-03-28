# Command Lines

This project now supports practical command-line workflows for both app runtime and builds.

## Build and run from source

```powershell
go mod tidy
go build -ldflags="-s -w -H windowsgui" -o spank.exe .
.\spank.exe
```

## App flags

Show help:

```powershell
.\spank.exe -h
```

Start GUI and auto-start listening:

```powershell
.\spank.exe -mode halo -autostart -sensitivity 6 -cooldown-ms 650
```

Headless detection (no GUI):

```powershell
.\spank.exe -headless -mode sexy -sensitivity 7 -run-for 1m
```

Use explicit threshold:

```powershell
.\spank.exe -mode pain -min-amp 0.08 -cooldown-ms 900
```

List modes:

```powershell
.\spank.exe -list-modes
```

## go run examples

```powershell
go run . -mode halo -autostart
```

```powershell
go run . -headless -mode pain -sensitivity 5 -run-for 30s
```

## build.bat options

Installer flow (existing behavior):

```bat
build.bat
```

Build-only flow:

```bat
build.bat --build-only
```

Help:

```bat
build.bat --help
```
