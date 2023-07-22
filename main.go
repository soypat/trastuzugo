package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
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

	usbSelector := &widget.Form{
		SubmitText: "Open port",
		Items: []*widget.FormItem{
			{Text: "Device", HintText: "Select a USB device", Widget: usbDevDropdown},
			{Text: "Baud", Widget: baudSelect},
			{Text: "Data Bits", Widget: databitsSelect},
			{Text: "Stop Bits", Widget: stopbitsSelect},
			{Text: "Parity", Widget: paritySelect},
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
			apptabs.Append(makeUSBTab(dev, rwc, apptabs))

			log.Println("Opened port", usbDevDropdown.Text)
		},
	}

	apptabs.Append(container.NewTabItem("OPEN", container.NewVBox(
		widget.NewButton("Refresh Devices", refreshUSBs),
		usbSelector,
	)))
	return apptabs
}

func makeUSBTab(devname string, rwc io.ReadWriteCloser, apptabs *container.AppTabs) *container.TabItem {
	// var usbBuffer [1024]byte
	closeButton := widget.NewButton("Close "+devname, func() {
		rwc.Close()
		apptabs.Remove(apptabs.Selected())
	})
	closeButton.Importance = widget.DangerImportance

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
	sendButton.OnTapped = func() {
		sendButton.Text = "In schedule call..."
		sendButton.Disable()
		go func() {
			defer func() {
				sendButton.Text = "Send data"
				sendButton.Enable()
			}()
		REPEAT:
			objects := append([]fyne.CanvasObject{}, schedules.Objects...)
			for i := 0; i < len(objects); i += 3 {
				duration, _ := time.ParseDuration(objects[i].(*widget.Entry).Text)
				text := objects[i+1].(*widget.Entry).Text
				n, err := rwc.Write([]byte(text))
				if n == 0 && err != nil {
					log.Println("Error writing to port", err)
					return
				}
				time.Sleep(duration)
			}
			if onRepeat.Checked {
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
	sender := container.NewHBox(sendButton, widget.NewButton("Add schedule", appendSchedule), onRepeat)
	return container.NewTabItem(devname, container.NewVBox(
		closeButton,
		scheduleTitle,
		scheduleScroll,
		sender,
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
	a.Lifecycle().SetOnEnteredForeground(func() {
		log.Println("Lifecycle: Entered Foreground")

	})
	a.Lifecycle().SetOnExitedForeground(func() {
		log.Println("Lifecycle: Exited Foreground")
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
