package d2xx

import (
	"fmt"
	"io"
	"time"

	"github.com/ysh86/ftPIC/d2xx/ftdi"
)

type Flash struct {
	devA     *device
	commands [8192 * 2]byte
}

func OpenFlash() (*Flash, error) {
	const (
		SUPPORTED = ftdi.FT2232H
	)

	num, err := numDevices()
	if err != nil {
		return nil, err
	}
	if num < 2 {
		return nil, fmt.Errorf("numDevices: %d", num)
	}

	// open 1st dev only
	devA, err := openDev(d2xxOpen, 0)
	if err != nil {
		return nil, err
	}
	if devA.t != SUPPORTED {
		devA.closeDev()
		return nil, fmt.Errorf("device is not %s, but %s", SUPPORTED, devA.t)
	}

	// configure devices for MPSSE
	err = devA.reset()
	if err != nil {
		devA.closeDev()
		return nil, err
	}
	err = devA.setupCommon()
	if err != nil {
		devA.closeDev()
		return nil, err
	}
	err = devA.setBitMode(0, bitModeMpsse)
	if err != nil {
		devA.closeDev()
		return nil, err
	}

	f := &Flash{devA: devA}
	time.Sleep(50 * time.Millisecond)

	// try MPSSE
	err = f.tryMpsse(f.devA)
	if err != nil {
		f.Close()
		return nil, err
	}

	// setup GPIO
	err = f.n64SetupPins()
	if err != nil {
		f.Close()
		return nil, err
	}

	// now ready to go
	err = f.n64ResetCart()
	if err != nil {
		f.Close()
		return nil, err
	}

	return f, nil
}

func (f *Flash) Close() {
	if f.devA != nil {
		f.devA.setBitMode(0, bitModeReset)
		f.devA.closeDev()
		f.devA = nil
	}
}

func (f *Flash) DevInfo() (ftdi.DevType, uint16, uint16) {
	return f.devA.t, f.devA.venID, f.devA.devID
}

func (f *Flash) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}

func (f *Flash) Read512(addr uint32) ([]byte, error) {
	err := f.n64SetAddress(addr)
	if err != nil {
		return nil, err
	}

	data, err := f.n64ReadROM512()
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (f *Flash) tryMpsse(dev *device) error {
	b := 0
	e := 0

	// Enable loopback
	f.commands[e] = 0x84
	e++
	sent, err := dev.write(f.commands[b:e])
	if err != nil {
		return err
	}
	if sent != e-b {
		return fmt.Errorf("failed to write command: 0x%02x", f.commands[b])
	}
	b++
	// Check the receive buffer is empty
	n, err := dev.read(f.commands[e : e+1])
	if n != 0 || err != nil {
		return fmt.Errorf("MPSSE receive buffer should be empty: n=%d, err=%w", n, err)
	}

	// Synchronize the MPSSE
	f.commands[e] = 0xab // bogus command
	e++
	_, err = dev.write(f.commands[b:e])
	b++
	for n == 0 && err == nil {
		n, err = dev.read(f.commands[e : e+2])
	}
	if err != nil {
		return err
	}
	if n != 2 || f.commands[e] != 0xfa || f.commands[e+1] != 0xab {
		return fmt.Errorf("failed to synchronize the MPSSE")
	}

	// Disable loopback
	f.commands[e] = 0x85
	e++
	sent, err = dev.write(f.commands[b:e])
	if err != nil {
		return err
	}
	if sent != e-b {
		return fmt.Errorf("failed to write command: 0x%02x", f.commands[b])
	}
	b++
	// Check the receive buffer is empty
	n, err = dev.read(f.commands[e : e+1])
	if n != 0 || err != nil {
		return fmt.Errorf("MPSSE receive buffer should be empty: n=%d, err=%w", n, err)
	}

	return nil
}

// n64 pins:
//
// Channel A:
// ADBUS0: TCK/SK: OUT (SPI SCLK)
// ADBUS1: TDI/DO: OUT (SPI MOSI)
// ADBUS2: TDO/DI: IN  (SPI MISO) // TODO: Not used. It should be output/Lo or loopback?
// ADBUS3: TMS/CS: OUT SPI CS -> Ch.B GPIOL1
// ADBUS4: GPIOL0: OUT /WE
// ADBUS5: GPIOL1: OUT /RE
// ADBUS6: GPIOL2: OUT ALE_L
// ADBUS7: GPIOL3: OUT ALE_H
//
// ACBUS0: GPIOH0: I/O AD0 (default: In)
// ACBUS1: GPIOH1: I/O AD1 (default: In)
// ACBUS2: GPIOH2: I/O AD2 (default: In)
// ACBUS3: GPIOH3: I/O AD3 (default: In)
// ACBUS4: GPIOH4: I/O AD4 (default: In)
// ACBUS5: GPIOH5: I/O AD5 (default: In)
// ACBUS6: GPIOH6: I/O AD6 (default: In)
// ACBUS7: GPIOH7: I/O AD7 (default: In)
//
// Channel B:
// BDBUS0: TCK/SK: OUT (SPI SCLK)
// BDBUS1: TDI/DO: OUT (SPI MOSI)
// BDBUS2: TDO/DI: IN  (SPI MISO) // TODO: Not used. It should be output/Lo or loopback?
// BDBUS3: TMS/CS: OUT (SPI CS)
// BDBUS4: GPIOL0: OUT /RST
// BDBUS5: GPIOL1: IN  WAIT for Ch.A
// BDBUS6: GPIOL2: OUT CLK
// BDBUS7: GPIOL3: IN  S_DAT // TODO: Not used. It should be output/Lo? or Pull-up.
//
// BCBUS0: GPIOH0: I/O AD8  (default: In)
// BCBUS1: GPIOH1: I/O AD9  (default: In)
// BCBUS2: GPIOH2: I/O AD10 (default: In)
// BCBUS3: GPIOH3: I/O AD11 (default: In)
// BCBUS4: GPIOH4: I/O AD12 (default: In)
// BCBUS5: GPIOH5: I/O AD13 (default: In)
// BCBUS6: GPIOH6: I/O AD14 (default: In)
// BCBUS7: GPIOH7: I/O AD15 (default: In)
func (f *Flash) n64SetupPins() error {
	b := 0
	e := 0

	// clock: master 60_000_000 / ((1+0x0002)*2) [Hz] = 10[MHz]
	// TODO: 7.5[MHz] for flash:3
	clockDivisorHi := uint8(0x00)
	clockDivisorLo := uint8(0x02)
	f.commands[e] = 0x8a // Use 60MHz master clock
	e++
	f.commands[e] = 0x97 // Turn off adaptive clocking
	e++
	f.commands[e] = 0x8c // Enable three-phase clocking for I2C EEPROM
	e++
	f.commands[e] = 0x86 // set clock divisor
	e++
	f.commands[e] = clockDivisorLo
	e++
	f.commands[e] = clockDivisorHi
	e++
	_, err := f.devA.write(f.commands[b:e])
	if err != nil {
		return err
	}
	b = e

	// pins A
	f.commands[e] = 0x80
	e++
	f.commands[e] = 0b1011_0001 // ALE_H:1, ALE_L:0, /RE:1, /WE:1, CS:0, (MISO:0, MOSI:0, SCLK:1)
	e++
	f.commands[e] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out, (MISO:In, MOSI:Out, SCLK:Out)
	e++
	f.commands[e] = 0x82
	e++
	f.commands[e] = 0x00 // AD7-0:0
	e++
	f.commands[e] = 0x00 // AD7-0:In
	e++
	_, err = f.devA.write(f.commands[b:e])
	if err != nil {
		return err
	}
	//b = e

	return nil
}

func (f *Flash) n64ResetCart() error {
	b := 0
	e := 0

	// pins B
	f.commands[e] = 0x80
	e++
	f.commands[e] = 0b0100_0001 // S_DAT, CLK, WAIT, /RST:0, CS
	e++
	f.commands[e] = 0b0101_1011 // S_DAT:In, CLK:Out, WAIT:In, /RST:Out, CS:Out
	e++
	_, err := f.devA.write(f.commands[b:e])
	if err != nil {
		return err
	}
	b = e
	f.commands[e] = 0x80
	e++
	f.commands[e] = 0b0101_0001 // S_DAT, CLK, WAIT, /RST:1, CS
	e++
	f.commands[e] = 0b0101_1011 // S_DAT:In, CLK:Out, WAIT:In, /RST:Out, CS:Out
	e++
	_, err = f.devA.write(f.commands[b:e])
	if err != nil {
		return err
	}

	time.Sleep(5 * time.Millisecond)

	return nil
}

func (f *Flash) n64SetAddress(addr uint32) error {
	bA := 0
	eA := 0
	bB := len(f.commands) / 2
	eB := len(f.commands) / 2

	// ALE_H/ALE_L = ?/? -> 0/0 -> wait -> 1/0 -> 1/1,CS:1 -> 1/1,CS:0
	// ALE_H/ALE_L = ?/? -> 0/0
	f.commands[eA] = 0x80
	eA++
	f.commands[eA] = 0b0011_0001 // ALE_H, ALE_L, /RE, /WE, CS
	eA++
	f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
	eA++
	// wait 0 =  1.6[us]
	// wait 1 =  2.8[us] (+1.2[us]  = 1.20u/byte = 150n/bit)
	// wait 2 =  4.0[us] (+2.4[us]  = 1.20u/byte = 150n/bit)
	// wait 4 =  6.6[us] (+5.0[us]  = 1.25u/byte = 156n/bit)
	// wait 9 = 12.5[us] (+10.9[us] = 1.21u/byte = 151n/bit)
	{
		f.commands[eA] = 0x8f // wait
		eA++
		f.commands[eA] = 9 // uint16 Lo
		eA++
		f.commands[eA] = 0 // uint16 Hi
		eA++
	}
	// ALE_H/ALE_L = 0/0 -> 1/0
	f.commands[eA] = 0x80
	eA++
	f.commands[eA] = 0b1011_0001 // ALE_H, ALE_L, /RE, /WE, CS
	eA++
	f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
	eA++
	// ALE_H/ALE_L = 1/0 -> 1/1, CS:0->1
	f.commands[eA] = 0x80
	eA++
	f.commands[eA] = 0b1111_1001 // ALE_H, ALE_L, /RE, /WE, CS
	eA++
	f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
	eA++
	// CS:1->0 for delay 200[ns]
	f.commands[eA] = 0x80
	eA++
	f.commands[eA] = 0b1111_0001 // ALE_H, ALE_L, /RE, /WE, CS
	eA++
	f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
	eA++

	// Wait On I/O High
	f.commands[eB] = 0x88
	eB++
	// for delay
	f.commands[eB] = 0x80
	eB++
	f.commands[eB] = 0b0101_0001 // S_DAT, CLK, WAIT, /RST, CS
	eB++
	f.commands[eB] = 0b0101_1011 // S_DAT:In, CLK:Out, WAIT:In, /RST:Out, CS:Out
	eB++

	// addr Hi
	f.commands[eB] = 0x82
	eB++
	f.commands[eB] = uint8(addr >> 24) // AD15-8
	eB++
	f.commands[eB] = 0xff // AD15-8:Out
	eB++
	f.commands[eA] = 0x82
	eA++
	f.commands[eA] = uint8((addr >> 16) & 0xff) // AD7-0
	eA++
	f.commands[eA] = 0xff // AD7-0:Out
	eA++
	// ALE_H/ALE_L = 1/1 -> 0/1, CS:0->1
	f.commands[eA] = 0x80
	eA++
	f.commands[eA] = 0b0111_1001 // ALE_H, ALE_L, /RE, /WE, CS
	eA++
	f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
	eA++
	// CS:1->0 for delay
	f.commands[eA] = 0x80
	eA++
	f.commands[eA] = 0b0111_0001 // ALE_H, ALE_L, /RE, /WE, CS
	eA++
	f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
	eA++

	// Wait On I/O High
	f.commands[eB] = 0x88
	eB++
	// for delay
	f.commands[eB] = 0x80
	eB++
	f.commands[eB] = 0b0101_0001 // S_DAT, CLK, WAIT, /RST, CS
	eB++
	f.commands[eB] = 0b0101_1011 // S_DAT:In, CLK:Out, WAIT:In, /RST:Out, CS:Out
	eB++

	// addr Lo
	f.commands[eB] = 0x82
	eB++
	f.commands[eB] = uint8((addr >> 8) & 0xff) // AD15-8
	eB++
	f.commands[eB] = 0xff // AD15-8:Out
	eB++
	f.commands[eA] = 0x82
	eA++
	f.commands[eA] = uint8(addr & 0xff) // AD7-0
	eA++
	f.commands[eA] = 0xff // AD7-0:Out
	eA++
	// ALE_H/ALE_L = 0/1 -> 0/0, CS:0->1
	f.commands[eA] = 0x80
	eA++
	f.commands[eA] = 0b0011_1001 // ALE_H, ALE_L, /RE, /WE, CS
	eA++
	f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
	eA++
	// CS:1->0 for delay
	f.commands[eA] = 0x80
	eA++
	f.commands[eA] = 0b0011_0001 // ALE_H, ALE_L, /RE, /WE, CS
	eA++
	f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
	eA++

	// Wait On I/O High
	f.commands[eB] = 0x88
	eB++
	// for delay
	f.commands[eB] = 0x80
	eB++
	f.commands[eB] = 0b0101_0001 // S_DAT, CLK, WAIT, /RST, CS
	eB++
	f.commands[eB] = 0b0101_1011 // S_DAT:In, CLK:Out, WAIT:In, /RST:Out, CS:Out
	eB++

	// Bus direction
	f.commands[eB] = 0x82
	eB++
	f.commands[eB] = 0x00 // AD15-8
	eB++
	f.commands[eB] = 0x00 // AD15-8:In
	eB++
	f.commands[eA] = 0x82
	eA++
	f.commands[eA] = 0x00 // AD7-0
	eA++
	f.commands[eA] = 0x00 // AD7-0:In
	eA++

	_, err := f.devA.write(f.commands[bB:eB])
	if err != nil {
		return err
	}
	_, err = f.devA.write(f.commands[bA:eA])
	if err != nil {
		return err
	}

	return nil
}

func (f *Flash) n64ReadROM512() ([]byte, error) {
	bA := 0
	eA := 0
	bB := len(f.commands) / 2
	eB := len(f.commands) / 2

	for i := 0; i < 256; i++ {
		// /RE:1->0
		f.commands[eA] = 0x80
		eA++
		f.commands[eA] = 0b0001_0001 // ALE_H, ALE_L, /RE, /WE, CS
		eA++
		f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
		eA++
		// TODO: for flash?
		// wait 15 = 1.6u + 150/bit * 8 * 15 = 19.6[us]
		if false {
			f.commands[eA] = 0x8f // wait
			eA++
			f.commands[eA] = 15 // uint16 Lo
			eA++
			f.commands[eA] = 0 // uint16 Hi
			eA++
		}
		// CS:0->1
		f.commands[eA] = 0x80
		eA++
		f.commands[eA] = 0b0001_1001 // ALE_H, ALE_L, /RE, /WE, CS
		eA++
		f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
		eA++
		// CS:1->0 for delay
		f.commands[eA] = 0x80
		eA++
		f.commands[eA] = 0b0001_0001 // ALE_H, ALE_L, /RE, /WE, CS
		eA++
		f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
		eA++

		// Wait On I/O High
		f.commands[eB] = 0x88
		eB++
		// for delay
		f.commands[eB] = 0x80
		eB++
		f.commands[eB] = 0b0101_0001 // S_DAT, CLK, WAIT, /RST, CS
		eB++
		f.commands[eB] = 0b0101_1011 // S_DAT:In, CLK:Out, WAIT:In, /RST:Out, CS:Out
		eB++

		// read
		f.commands[eB] = 0x83 // AD15-8
		eB++
		f.commands[eA] = 0x83 // AD7-0
		eA++

		// /RE:0->1
		f.commands[eA] = 0x80
		eA++
		f.commands[eA] = 0b0011_0001 // ALE_H, ALE_L, /RE, /WE, CS
		eA++
		f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
		eA++
		// for delay
		f.commands[eA] = 0x80
		eA++
		f.commands[eA] = 0b0011_0001 // ALE_H, ALE_L, /RE, /WE, CS
		eA++
		f.commands[eA] = 0b1111_1011 // ALE_H:Out, ALE_L:Out, /RE:Out, /WE:Out, CS:Out
		eA++
	}

	_, err := f.devA.write(f.commands[bB:eB])
	if err != nil {
		return nil, err
	}
	bB = eB
	eB += 256
	_, err = f.devA.write(f.commands[bA:eA])
	if err != nil {
		return nil, err
	}
	bA = eA
	eA += 256

	err = f.devA.readAll(f.commands[bB:eB])
	if err != nil {
		return nil, err
	}
	err = f.devA.readAll(f.commands[bA:eA])
	if err != nil {
		return nil, err
	}

	// interleave B(hi) and A(lo)
	result := f.commands[eB : eB+512]
	for i := 0; i < 256; i++ {
		result[i*2+0] = f.commands[bB+i]
		result[i*2+1] = f.commands[bA+i]
	}

	return result, nil
}
