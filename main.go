package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/felixge/pidctrl"
	"golang.org/x/exp/io/i2c"
	// http://blog.mavtechglobal.com/blog/2013/01/15/initial-settings-for-pid-controllers
)

const (
	LCD_CHR = 1 // Mode - Sending data
	LCD_CMD = 0 // Mode - Sending command

	LCD_ENTRYLEFT      = 0x02
	LCD_DISPLAYON      = 0x04
	LCD_CURSOROFF      = 0x00
	LCD_BLINKOFF       = 0x00
	LCD_2LINE          = 0x08
	LCD_5x8DOTS        = 0x00
	LCD_BACKLIGHT      = 0x08 // backlight on?
	LCD_ENTRYMODESET   = 0x04
	LCD_CLEARDISPLAY   = 0x01
	LCD_RETURNHOME     = 0x02
	LCD_FUNCTIONSET    = 0x20
	LCD_DISPLAYCONTROL = 0x08
	LCD_LINE_1         = 0x80
	LCD_LINE_2         = 0xC0
	LCD_LINE_3         = 0x94
	LCD_LINE_4         = 0xD4
)

type tempReading struct {
	busid string
	temp  float64
}

type Batch struct {
	busid  string
	temp   float64
	target float64
	pidset float64
	start  time.Time
	pc     *pidctrl.PIDController
}

func getTemp(path string, responses chan tempReading) {
	dat, err := ioutil.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(dat), "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[0], "YES") {
		return
	}
	p := strings.Split(path, "/")
	if len(p) < 6 || len(p[5]) < 5 {
		return
	}
	busid := p[5]
	if temp, err := strconv.Atoi(lines[1][len(lines[1])-5:]); err == nil {
		responses <- tempReading{busid: busid, temp: float64(temp) / 1000.0}
	}
}

func initLCD(devpath string) func(b byte, mode byte) {
	dev := i2c.Devfs{Dev: devpath}
	lcd, err := i2c.Open(&dev, 0x27)
	if err != nil {
		panic("error opening i2c device: " + err.Error())
	}
	send := func(b byte, mode byte) {
		bh := (b & 0xF0)
		bl := ((b & 0x0F) << 4)

		lcd.Write([]byte{bh | LCD_BACKLIGHT | mode})
		time.Sleep(25 * time.Microsecond)
		lcd.Write([]byte{bh | 4 | LCD_BACKLIGHT | mode})
		time.Sleep(50 * time.Microsecond)
		lcd.Write([]byte{bl | LCD_BACKLIGHT | mode})
		time.Sleep(25 * time.Microsecond)
		lcd.Write([]byte{bl | 4 | LCD_BACKLIGHT | mode})
		time.Sleep(50 * time.Microsecond)
		lcd.Write([]byte{LCD_BACKLIGHT | mode})
		time.Sleep(25 * time.Microsecond)
	}
	send(0x33, LCD_CMD)                                                        // not sure, some sort of init code
	send(0x32, LCD_CMD)                                                        // same
	send(LCD_ENTRYMODESET|LCD_ENTRYLEFT, LCD_CMD)                              // cursor move direction
	send(LCD_DISPLAYCONTROL|LCD_DISPLAYON|LCD_CURSOROFF|LCD_BLINKOFF, LCD_CMD) // Display On,Cursor Off, Blink Off
	send(LCD_FUNCTIONSET|LCD_2LINE|LCD_5x8DOTS, LCD_CMD)                       // Data length, number of lines, font size
	send(LCD_CLEARDISPLAY, LCD_CMD)
	send(LCD_RETURNHOME, LCD_CMD)
	return send
}

func main() {
	sendLCD := initLCD("/dev/i2c-1")
	temps := make(chan tempReading)
	batchMap := make(map[string]*Batch)
	updateLCD := time.Tick(time.Second * 5)
	updateTemps := time.Tick(time.Second * 10)
	for {
		select {
		case tr := <-temps:
			fmt.Println(tr.busid, tr.temp)
			if _, ok := batchMap[tr.busid]; !ok {
				batchMap[tr.busid] = &Batch{
					busid:  tr.busid,
					temp:   tr.temp,
					target: 28.0,
					pc:     pidctrl.NewPIDController(1.0, 3.0, 0.2),
					start:  time.Now(),
				}
				batchMap[tr.busid].pc.SetOutputLimits(0.0, 100.0)
				batchMap[tr.busid].pc.Set(float64(batchMap[tr.busid].target))
			}
			batchMap[tr.busid].temp = tr.temp
			batchMap[tr.busid].pidset = batchMap[tr.busid].pc.Update(tr.temp)
			fmt.Println("PID VAL", batchMap[tr.busid].pidset)
		case <-updateTemps:
			if ms, err := filepath.Glob("/sys/bus/w1/devices/*/w1_slave"); err == nil {
				for _, m := range ms {
					go getTemp(m, temps)
				}
			}
		case <-updateLCD:
			for _, batch := range batchMap {
				since := time.Since(batch.start)
				duration := fmt.Sprintf("% 2d:%02d:%02d:%02d", int(since.Hours())/24, int(since.Hours())%24,
					int(since.Minutes())%60, int(since.Seconds())%60)
				ftemp := float64(batch.temp)*9.0/5.0 + 32.0
				msg := fmt.Sprintf("% 3.2f\xdfF%12s", ftemp, duration)
				sendLCD(LCD_LINE_1, LCD_CMD)
				for _, c := range []byte(msg) {
					sendLCD(byte(c), LCD_CHR)
				}
			}
		}
	}
}
