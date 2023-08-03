package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/cmd/fyne_settings/settings"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/soypat/cereal"
	"go.bug.st/serial/enumerator"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slog"
)

//	var v = &fyne.StaticResource{
//		StaticName:    "fyne.png",
//		StaticContent: []byte(""),
//	}

var topwindow fyne.Window

type usbControl struct {
	opener cereal.Opener
}

func main() {
	a := app.NewWithID("trastuzu.go")
	var opener = &usbControl{
		opener: cereal.Bugst{},
	}
	a.SetIcon(theme.FyneLogo())
	makeTray(a)
	logLifecycle(a)
	w := a.NewWindow("Comms - Trastuzugo")
	topwindow = w
	w.SetMainMenu(makeMenu(a, w))
	w.SetMaster()

	w.SetContent(container.NewVBox(
		makeusblogger(opener),
	))

	w.Resize(fyne.NewSize(640, 460))
	w.ShowAndRun()
}

func makeusblogger(opener *usbControl) fyne.CanvasObject {
	apptabs := container.NewAppTabs()
	usbDevDropdown := widget.NewSelectEntry(nil)

	refreshUSBs := func() {
		plist, err := enumerator.GetDetailedPortsList()
		if err != nil {
			log.Println("Error listing ports", err)
			return
		}
		var list []string
		for _, port := range plist {
			name := port.Name
			if port.Product != "" {
				name += " (" + port.Product + ")"
			} else if port.SerialNumber != "" {
				name += " (" + port.SerialNumber + ")"
			} else if port.VID != "" && port.PID != "" {
				name += " (vid=" + port.VID + ", pid=" + port.PID + ")"
			}
			list = append(list, name)
		}
		usbDevDropdown.SetOptions(list)
		if len(list) > 0 {
			usbDevDropdown.SetText(list[0])
		} else {
			usbDevDropdown.SetText("none available")
		}
		usbDevDropdown.Refresh()
	}
	refreshUSBs()

	baudSelect := widget.NewSelectEntry([]string{"1200", "4800", "9600", "19200", "38400", "57600", "115200"})
	baudSelect.SetText("115200")
	baudSelect.Validator = intValidator
	stopbitsSelect := widget.NewSelectEntry([]string{"1", "2"})
	stopbitsSelect.SetText("1")
	stopbitsSelect.Validator = intValidator
	paritySelect := widget.NewSelectEntry([]string{"None", "Odd", "Even", "Mark", "Space"})
	paritySelect.SetText("None")
	paritySelect.Validator = validateParity
	databitsSelect := widget.NewSelectEntry([]string{"5", "6", "7", "8"})
	databitsSelect.SetText("8")
	databitsSelect.Validator = intValidator
	outputdir, err := os.UserHomeDir()
	if err != nil {
		outputdir = os.TempDir()
	}
	logFormattedName := filepath.Join(outputdir, "trastuzugo-"+time.Kitchen+".log")
	saveToLog := widget.NewCheck("Save output to log: "+logFormattedName, nil)

	usbSelector := &widget.Form{
		SubmitText: "Open port",
		Items: []*widget.FormItem{
			{Text: "Device", HintText: "Select a USB device", Widget: usbDevDropdown},
			{Text: "Baud", Widget: baudSelect},
			{Text: "Data Bits", Widget: databitsSelect},
			{Text: "Stop Bits", Widget: stopbitsSelect},
			{Text: "Parity", Widget: paritySelect},
			{Text: "Save to log", Widget: saveToLog},
		},
		OnSubmit: func() {
			baud, err := strconv.Atoi(baudSelect.Text)
			if err != nil {
				log.Println("Error parsing baud", err)
				return
			}
			stopbits, err := strconv.Atoi(stopbitsSelect.Text)
			if err != nil {
				log.Println("Error parsing stopbits", err)
				return
			}
			stopbits = stopbits/2 + b2i(stopbits == 2)
			databits, err := strconv.Atoi(databitsSelect.Text)
			if err != nil {
				log.Println("Error parsing databits", err)
				return
			}
			p, err := parseParity(paritySelect.Text)
			if err != nil {
				log.Println("Error parsing parity", err)
				return
			}
			mode := cereal.Mode{
				BaudRate: baud,
				DataBits: databits,
				Parity:   p,
				StopBits: cereal.StopBits(stopbits),
			}
			dev, _, _ := strings.Cut(usbDevDropdown.Text, " (")
			rwc, err := opener.opener.OpenPort(dev, mode)
			if err != nil {
				log.Println("Error opening port", err)
				return
			}
			rwc = &readWriteLogger{
				rwc: rwc,
				log: slog.Default(),
			}
			var logname string
			if saveToLog.Checked {
				logname = time.Now().Format("trastuzugo-" + time.Kitchen + ".log")
			}
			apptabs.Append(makeUSBTab(dev, rwc, apptabs, logname))

			log.Println("Opened port", usbDevDropdown.Text)
		},
	}

	apptabs.Append(container.NewTabItem("OPEN", container.NewVBox(
		widget.NewButton("Refresh Devices", refreshUSBs),
		usbSelector,
	)))
	return apptabs
}

func makeUSBTab(devname string, rwc io.ReadWriteCloser, apptabs *container.AppTabs, logname string) *container.TabItem {
	ctx, cancel := context.WithCancel(context.Background())
	if logname != "" {
		go func() {
			defer log.Println("stopped logging to", logname)
			var buf [1024]byte
			fp, err := os.Create(logname)
			if err != nil {
				log.Println("Error creating log file", err)
				return
			}
			for ctx.Err() == nil {
				n, _ := rwc.Read(buf[:])
				if n == 0 {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				log.Println("writing", n, "bytes to log file")
				_, err = fp.Write(buf[:n])
				if err != nil {
					log.Println("Error writing to log file", err)
					return
				}
			}
		}()
	}

	closeButton := widget.NewButton("Close "+devname, func() {
		log.Println("close button pressed for " + devname)
		cancel()
		rwc.Close()
		apptabs.Remove(apptabs.Selected())
	})
	closeButton.Importance = widget.DangerImportance
	escapeSelect := widget.NewSelectEntry(maps.Keys(availableEscapes))
	escapeSelect.SetText("No escaping")
	escapeSelect.Validator = func(s string) error {
		if _, ok := availableEscapes[s]; !ok {
			return errors.New("unknown escape: " + s)
		}
		return nil
	}

	schedules := container.NewGridWithColumns(3)
	appendSchedule := func() {
		duration := widget.NewEntry()
		duration.SetText("0s")
		duration.Validator = durationValidator
		duration.TextStyle.Monospace = true
		dataentry := widget.NewMultiLineEntry()
		removeButton := widget.NewButton("Remove", nil)
		removeButton.OnTapped = func() {
			schedules.Remove(duration)
			schedules.Remove(dataentry)
			schedules.Remove(removeButton)
			schedules.Refresh()
		}
		removeButton.Icon = theme.DeleteIcon()
		removeButton.Importance = widget.DangerImportance
		dataentry.SetMinRowsVisible(2)
		dataentry.TextStyle.Monospace = true
		dataentry.Validator = func(s string) error {
			esc, ok := availableEscapes[escapeSelect.Text]
			if !ok || esc == nil {
				return errors.New("choose valid escape")
			}
			_, err := esc(s)
			return err
		}
		schedules.Add(duration)
		schedules.Add(dataentry)
		schedules.Add(removeButton)

		schedules.Refresh()
	}

	appendSchedule()
	scheduleScroll := container.NewVScroll(container.NewVBox(schedules))
	scheduleScroll.SetMinSize(fyne.NewSize(0, 200))
	sendButton := widget.NewButton("Send data", nil)
	onRepeat := widget.NewCheck("Repeat", nil)
	precedLFwithCR := widget.NewCheck("Precede newlines with CR (\\r)", nil)
	sendButton.OnTapped = func() {
		escape, ok := availableEscapes[escapeSelect.Text]
		if !ok || escape == nil {
			log.Println("Unknown escape", escapeSelect.Text)
			return
		}
		sendButton.Text = "In schedule call..."
		sendButton.Disable()
		go func() {
			defer func() {
				sendButton.Text = "Send data"
				sendButton.Enable()
			}()
			repeated := 0
		REPEAT:
			objects := append([]fyne.CanvasObject{}, schedules.Objects...)
			start := time.Now()
			for i := 0; i < len(objects); i += 3 {
				duration, _ := time.ParseDuration(objects[i].(*widget.Entry).Text)
				text := objects[i+1].(*widget.Entry).Text
				escapedText, err := escape(text)
				if err != nil {
					log.Println("Error escaping text", err)
					return
				}
				if precedLFwithCR.Checked {
					escapedText = bytes.ReplaceAll(escapedText, []byte("\n"), []byte("\r\n"))
				}
				n, err := rwc.Write(escapedText)
				if n == 0 && err != nil {
					log.Println("Error writing to port", err)
					return
				}
				time.Sleep(duration)
			}
			repeated++
			if onRepeat.Checked {
				if repeated%1000 == 0 && time.Since(start) > time.Millisecond {
					time.Sleep(time.Millisecond) // Sleep a millisecond to not hog the CPU every 1000 iterations.
				}
				goto REPEAT
			}
		}()
	}

	scheduleTitle := container.NewGridWithColumns(3)
	scheduleTitle.Objects = []fyne.CanvasObject{
		widget.NewLabel("Duration (hold time)"),
		widget.NewLabel("Data"),
		widget.NewLabel(""),
	}

	sender := container.NewHBox(sendButton, widget.NewButton("Add schedule", appendSchedule), widget.NewLabel("Escapes:"), escapeSelect)
	checkboxes := container.NewHBox(onRepeat, precedLFwithCR)
	return container.NewTabItem(devname, container.NewVBox(
		closeButton,
		scheduleTitle,
		scheduleScroll,
		sender,
		checkboxes,
	))
}

func makeMenu(a fyne.App, w fyne.Window) *fyne.MainMenu {
	openSettings := func() {
		w := a.NewWindow("Fyne Settings")
		w.SetContent(settings.NewSettings().LoadAppearanceScreen(w))
		w.Resize(fyne.NewSize(480, 480))
		w.Show()
	}
	settingsItem := fyne.NewMenuItem("Settings", openSettings)
	settingsShortcut := &desktop.CustomShortcut{KeyName: fyne.KeyComma, Modifier: fyne.KeyModifierShortcutDefault}
	settingsItem.Shortcut = settingsShortcut
	w.Canvas().AddShortcut(settingsShortcut, func(shortcut fyne.Shortcut) {
		openSettings()
	})

	sourceCode := fyne.NewMenuItem("Source code", func() {
		u, _ := url.Parse("https://github.com/soypat/trastuzugo")
		_ = a.OpenURL(u)
	})
	// sourceCode.Icon
	helpMenu := fyne.NewMenu("Help",
		sourceCode,
	)

	// a quit item will be appended to our first (File) menu
	file := fyne.NewMenu("File")
	device := fyne.CurrentDevice()
	if !device.IsMobile() && !device.IsBrowser() {
		file.Items = append(file.Items, fyne.NewMenuItemSeparator(), settingsItem)
	}
	main := fyne.NewMainMenu(
		file,
		helpMenu,
	)
	return main
}

func logLifecycle(a fyne.App) {
	a.Lifecycle().SetOnStarted(func() {
		log.Println("Lifecycle: Started")
		fmt.Println("numgoroutine", runtime.NumGoroutine())
	})
	a.Lifecycle().SetOnStopped(func() {
		log.Println("Lifecycle: Stopped")
		fmt.Println("numgoroutine", runtime.NumGoroutine())
	})
}

func makeTray(a fyne.App) {
	if desk, ok := a.(desktop.App); ok {
		h := fyne.NewMenuItem("Bring to front", func() {})
		h.Icon = theme.HomeIcon()
		menu := fyne.NewMenu("Tray menu", h)
		h.Action = func() {
			topwindow.RequestFocus()
			menu.Refresh()
		}
		desk.SetSystemTrayMenu(menu)
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

type readWriteLogger struct {
	rwc io.ReadWriter
	log *slog.Logger
}

func (rwl *readWriteLogger) Read(p []byte) (n int, err error) {
	rwl.log.LogAttrs(context.Background(), slog.LevelDebug, "prep read "+strconv.Itoa(len(p)))
	n, err = rwl.rwc.Read(p)
	rwl.log.LogAttrs(context.Background(), slog.LevelInfo, "read "+strconv.Itoa(n), slog.String("data", string(p[:n])))
	return n, err
}

func (rwl *readWriteLogger) Write(p []byte) (n int, err error) {
	rwl.log.LogAttrs(context.Background(), slog.LevelDebug, "prep write "+strconv.Itoa(len(p)))
	n, err = rwl.rwc.Write(p)
	rwl.log.LogAttrs(context.Background(), slog.LevelInfo, "wrote "+strconv.Itoa(n), slog.String("data", string(p[:n])))
	return n, err
}

func (rwl *readWriteLogger) Close() error {
	if rwc, ok := rwl.rwc.(io.Closer); ok {
		return rwc.Close()
	}
	return nil
}

func intValidator(s string) error {
	_, err := strconv.Atoi(s)
	return err
}
func durationValidator(s string) error {
	d, err := time.ParseDuration(s)
	if d < 0 {
		return fmt.Errorf("duration must be positive")
	}
	return err
}

func validateParity(s string) error {
	_, err := parseParity(s)
	return err
}

func parseParity(s string) (p cereal.Parity, err error) {
	s = strings.ToLower(s)
	switch s {
	case "none":
		p = cereal.ParityNone
	case "odd":
		p = cereal.ParityOdd
	case "even":
		p = cereal.ParityEven
	case "mark":
		p = cereal.ParityMark
	case "space":
		p = cereal.ParitySpace
	default:
		return p, errors.New("unknown parity: " + s)
	}

	return p, nil
}

var availableEscapes = map[string]func(string) ([]byte, error){
	"No escaping": func(s string) ([]byte, error) { return []byte(s), nil },
	"C-style":     escapesCStyle,
	"Hex":         escapesHex,
}

func escapesCStyle(s string) ([]byte, error) {
	const quotes = `"`
	s = strings.ReplaceAll(s, quotes, `\"`)
	s, err := strconv.Unquote(quotes + s + quotes)
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

func escapesHex(s string) ([]byte, error) {
	s = strings.ReplaceAll(s, " ", "")
	return hex.DecodeString(s)
}
