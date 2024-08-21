package d2xx

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ysh86/ftPIC/d2xx/ftdi"
)

type Flash struct {
	// reader/writer
	devA     *device
	commands [8192 * 2]byte

	// target
	UserIDs       [32][2]byte
	Configuration [10]byte
	DeviceID      uint16
	RevisionID    uint16
	RevisionMajor string
	RevisionMinor uint8

	// target: Program Flash Memory
	lenPFM int
	posPFM int
}

// Setting multiple bits is valid.
type Region uint32

const (
	REGION_DATA_EEPROM   Region = 0b0001
	REGION_FLASH         Region = 0b0010
	REGION_USER_ID       Region = 0b0100
	REGION_CONFIGURATION Region = 0b1000
)

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
	err = f.setupPICPins()
	if err != nil {
		f.Close()
		return nil, err
	}

	// now ready to go
	err = f.resetPIC()
	if err != nil {
		f.Close()
		return nil, err
	}

	return f, nil
}

func (f *Flash) Close() error {
	if f.devA != nil {
		b := 0
		e := 0

		// /MCLR high
		f.commands[e] = 0x80
		e++
		f.commands[e] = 0b1000_0001 // /MCLR:1,   state:0,   ICSPDAT:0,   ICSPCLK:0
		e++
		f.commands[e] = 0b1111_1011 // /MCLR:Out, state:Out, ICSPDAT:Out, ICSPCLK:Out
		e++
		e = f.pushDelay(2, e)
		f.devA.write(f.commands[b:e])

		f.devA.setBitMode(0, bitModeReset)
		f.devA.closeDev()
		f.devA = nil
	}
	return nil
}

func (f *Flash) Read(p []byte) (n int, err error) {
	if f.posPFM >= f.lenPFM {
		return n, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}

	bytes := len(p)
	if f.posPFM+bytes > f.lenPFM {
		bytes = f.lenPFM - f.posPFM
	}
	words := (bytes + 1) >> 1

	err = f.loadAddress(uint32(f.posPFM))
	if err != nil {
		return n, err
	}

	i := 0
	for bytes&1 == 0 && words-i >= 64 {
		values, err := f.read64Words()
		if err != nil {
			return n, err
		}
		f.posPFM += 64 * 2

		copy(p, values)
		p = p[64*2:]
		n += 64 * 2

		i += 64
	}
	for i < words {
		value16, err := f.readWord()
		if err != nil {
			return n, err
		}
		f.posPFM += 2

		p[i*2+0] = byte(value16[0])
		n += 1
		if bytes&1 == 0 {
			p[i*2+1] = byte(value16[1])
			n += 1
		}

		i++
	}
	return n, nil
}

func (f *Flash) Seek(offset int64, whence int) (int64, error) {
	if offset&1 == 1 {
		return 0, errors.New("Seek: invalid offset: odd number")
	}

	switch whence {
	default:
		return 0, errors.New("Seek: invalid whence")
	case io.SeekStart:
		f.posPFM = int(offset)
	case io.SeekCurrent:
		f.posPFM += int(offset)
	}

	if f.posPFM < 0 || f.lenPFM <= f.posPFM {
		return 0, errors.New("Seek: invalid offset")
	}

	return int64(f.posPFM), nil
}

func (f *Flash) BulkErase(regions Region) error {
	b := 0
	e := 0

	// Bulk Erase: 0x18
	e = f.pushByte(0x18, e)
	e = f.pushDelay(2, e)

	r4_1 := (regions << 1) // 4:regions, 0:Stop bit
	e = f.pushByte(byte((r4_1>>16)&0xff), e)
	e = f.pushByte(byte((r4_1>>8)&0xff), e)
	e = f.pushByte(byte((r4_1>>0)&0xff), e)

	// T ERAB: 11[msec]
	e = f.pushDelayMillisecond(11, e)

	_, err := f.devA.write(f.commands[b:e])
	if err != nil {
		return err
	}

	return nil
}

func (f *Flash) WriterInfo() (ftdi.DevType, uint16, uint16) {
	return f.devA.t, f.devA.venID, f.devA.devID
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

// PIC pins:
//
// Channel A:
// ADBUS0: TCK/SK: OUT (SPI SCLK)
// ADBUS1: TDI/DO: OUT (SPI MOSI)
// ADBUS2: TDO/DI: IN  (SPI MISO) // TODO: Not used. It should be output/Lo or loopback?
// ADBUS3: TMS/CS: OUT (SPI CS)
// ADBUS4: GPIOL0: OUT ICSPCLK
// ADBUS5: GPIOL1: I/O ICSPDAT
// ADBUS6: GPIOL2: OUT (not used)
// ADBUS7: GPIOL3: OUT /MCLR
//
// ACBUS0: GPIOH0: OUT (not used)
// ACBUS1: GPIOH1: OUT (not used)
// ACBUS2: GPIOH2: OUT (not used)
// ACBUS3: GPIOH3: OUT (not used)
// ACBUS4: GPIOH4: OUT (not used)
// ACBUS5: GPIOH5: OUT (not used)
// ACBUS6: GPIOH6: OUT (not used)
// ACBUS7: GPIOH7: OUT (not used)
//
// Channel B: ASYNC Serial (RS232)
func (f *Flash) setupPICPins() error {
	b := 0
	e := 0

	// clock: master 60_000_000 / ((1+0x000e)*2) [Hz] = 2[MHz]
	clockDivisorHi := uint8(0x00)
	clockDivisorLo := uint8(0x0e)
	f.commands[e] = 0x8a // Use 60MHz master clock
	e++
	f.commands[e] = 0x97 // Turn off adaptive clocking
	e++
	f.commands[e] = 0x8d // Disable three-phase clocking for I2C EEPROM
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

	// init pins
	f.commands[e] = 0x80
	e++
	f.commands[e] = 0b1000_0001 // /MCLR:1,   state:0,   ICSPDAT:0,   ICSPCLK:0,   (CS:0,   MISO:0,  MOSI:0,   SCLK:1)
	e++
	f.commands[e] = 0b1111_1011 // /MCLR:Out, state:Out, ICSPDAT:Out, ICSPCLK:Out, (CS:Out, MISO:In, MOSI:Out, SCLK:Out)
	e++
	f.commands[e] = 0x82
	e++
	f.commands[e] = 0x00 // state:0
	e++
	f.commands[e] = 0b1111_1111 // direction:Out
	e++
	e = f.pushDelay(1, e)
	_, err = f.devA.write(f.commands[b:e])
	if err != nil {
		return err
	}
	//b = e

	return nil
}

func (f *Flash) resetPIC() error {
	b := 0
	e := 0

	// /MCLR low
	f.commands[e] = 0x80
	e++
	f.commands[e] = 0b0000_0001 // /MCLR:0,   state:0,   ICSPDAT:0,   ICSPCLK:0
	e++
	f.commands[e] = 0b1111_1011 // /MCLR:Out, state:Out, ICSPDAT:Out, ICSPCLK:Out
	e++
	_, err := f.devA.write(f.commands[b:e])
	if err != nil {
		return err
	}

	// T ENTH: 1[msec]
	e = f.pushDelayMillisecond(10, e)
	// The key sequence
	key32 := []byte{'M', 'C', 'H', 'P'}
	for _, k := range key32 {
		e = f.pushByte(k, e)
	}
	// T ENTH: 1[msec]
	e = f.pushDelayMillisecond(10, e)
	_, err = f.devA.write(f.commands[b:e])
	if err != nil {
		return err
	}

	// User IDs (32 Words)
	err = f.loadAddress(0x20_0000)
	if err != nil {
		return err
	}
	for i := 0; i < 32; i++ {
		value16, err := f.readWord()
		if err != nil {
			return err
		}
		f.UserIDs[i] = value16
	}

	// Configuration Bytes (10 Bytes)
	err = f.loadAddress(0x30_0000)
	if err != nil {
		return err
	}
	for i := 0; i < 10; i++ {
		value8, err := f.readByte()
		if err != nil {
			return err
		}
		f.Configuration[i] = value8
	}

	// Revision ID (1 Word), Device ID (1 Word)
	err = f.loadAddress(0x3f_fffc)
	if err != nil {
		return err
	}
	value16, err := f.readWord()
	if err != nil {
		return err
	}
	f.RevisionID = (uint16(value16[0]) | (uint16(value16[1]) << 8))
	f.RevisionMajor = string(rune('A' + ((f.RevisionID >> 6) & 0b11_1111)))
	f.RevisionMinor = uint8(f.RevisionID & 0b11_1111)
	value16, err = f.readWord()
	if err != nil {
		return err
	}
	f.DeviceID = (uint16(value16[0]) | (uint16(value16[1]) << 8))

	// reset
	err = f.loadAddress(0)
	if err != nil {
		return err
	}

	f.posPFM = 0

	switch f.DeviceID {
	case 0x74A0:
		f.lenPFM = 0x2_0000
	default:
		return errors.New("unknown target device")
	}

	return nil
}

// send a byte from MSB
func (f *Flash) pushByte(data byte, pos int) int {
	for i := 7; i >= 0; i-- {
		b := byte((data >> i) & 1)

		f.commands[pos] = 0x80
		pos++
		f.commands[pos] = ((b << 5) | 0b0001_0001) // /MCLR:0,   state:0,   ICSPDAT:b,   ICSPCLK:1
		pos++
		f.commands[pos] = 0b1111_1011 //              /MCLR:Out, state:Out, ICSPDAT:Out, ICSPCLK:Out
		pos++

		f.commands[pos] = 0x80
		pos++
		f.commands[pos] = ((b << 5) | 0b0000_0001) // /MCLR:0,   state:0,   ICSPDAT:b,   ICSPCLK:0
		pos++
		f.commands[pos] = 0b1111_1011 //              /MCLR:Out, state:Out, ICSPDAT:Out, ICSPCLK:Out
		pos++
	}
	return pos
}

// delay (0-7 + 1) clocks
//
// 1 clock @ 2[MHz] -> 500 nsec
func (f *Flash) pushDelay(clk byte, pos int) int {
	f.commands[pos] = 0x8e // wait
	pos++
	f.commands[pos] = clk - 1
	pos++
	return pos
}

// delay usec (>=2)
//
// 2 clocks @ 2[MHz] -> 1 usec
func (f *Flash) pushDelayMicrosecond(usec int, pos int) int {
	length := (2 * usec * 2 /* margin */) >> 3 // /8: to bytes

	f.commands[pos] = 0x8f // wait
	pos++
	f.commands[pos] = uint8(length & 0xff)
	pos++
	f.commands[pos] = uint8((length >> 8) & 0xff)
	pos++
	return pos
}

// delay msec
//
// 2000 clocks @ 2[MHz] -> 1 msec
func (f *Flash) pushDelayMillisecond(msec int, pos int) int {
	length := (2000 * msec * 2 /* margin */) >> 3 // /8: to bytes

	f.commands[pos] = 0x8f // wait
	pos++
	f.commands[pos] = uint8(length & 0xff)
	pos++
	f.commands[pos] = uint8((length >> 8) & 0xff)
	pos++
	return pos
}

func (f *Flash) pushReadWord(pos int) int {
	// Read Data from NVM & PC++: 0xfe
	pos = f.pushByte(0xfe, pos) // +6

	// ICSPDAT: Out -> In // +3
	{
		f.commands[pos] = 0x80
		pos++
		f.commands[pos] = 0b0000_0001 // /MCLR:0,   state:0,   ICSPDAT:0,   ICSPCLK:0
		pos++
		f.commands[pos] = 0b1101_1011 // /MCLR:Out, state:Out, ICSPDAT:In,  ICSPCLK:Out
		pos++
	}
	pos = f.pushDelay(2, pos) // +2

	for i := 23; i >= 0; i-- { // +7/loop
		// clock: high
		f.commands[pos] = 0x80
		pos++
		f.commands[pos] = 0b0001_0001 // /MCLR:0,   state:0,   ICSPDAT:0,   ICSPCLK:1
		pos++
		f.commands[pos] = 0b1101_1011 // /MCLR:Out, state:Out, ICSPDAT:In,  ICSPCLK:Out
		pos++

		// read
		f.commands[pos] = 0x81
		pos++

		// clock: low
		f.commands[pos] = 0x80
		pos++
		f.commands[pos] = 0b0000_0001 // /MCLR:0,   state:0,   ICSPDAT:0,   ICSPCLK:0
		pos++
		f.commands[pos] = 0b1101_1011 // /MCLR:Out, state:Out, ICSPDAT:In,  ICSPCLK:Out
		pos++
	}

	// ICSPDAT: In -> Out // +3
	{
		f.commands[pos] = 0x80
		pos++
		f.commands[pos] = 0b0000_0001 // /MCLR:0,   state:0,   ICSPDAT:0,   ICSPCLK:0
		pos++
		f.commands[pos] = 0b1111_1011 // /MCLR:Out, state:Out, ICSPDAT:Out, ICSPCLK:Out
		pos++
	}
	pos = f.pushDelay(2, pos) // +2

	// 6+3+2 + 7*24 + 3+2 = 184/16bits
	return pos
}

func (f *Flash) pushWriteWord(value16 []byte, pos int) int {
	if len(value16) < 2 {
		return pos
	}
	if value16[0] == 0xff && value16[1] == 0xff {
		// Increment Address: 0xf8
		pos = f.pushByte(0xf8, pos)
		pos = f.pushDelay(2, pos)
		return pos
	}

	// Program Data & PC++: 0xe0
	pos = f.pushByte(0xe0, pos)
	pos = f.pushDelay(2, pos)

	// swap
	value7_16_1 := (uint32(value16[1]) << 9) | (uint32(value16[0]) << 1) // 0:Start bit, 16:value, 0:Stop bit
	pos = f.pushByte(byte((value7_16_1>>16)&0xff), pos)
	pos = f.pushByte(byte((value7_16_1>>8)&0xff), pos)
	pos = f.pushByte(byte((value7_16_1>>0)&0xff), pos)

	// T PINT: 75[usec]
	pos = f.pushDelayMicrosecond(75, pos)

	return pos
}

func (f *Flash) pushWriteByte(value8 byte, pos int) int {
	if value8 == 0xff {
		// Increment Address: 0xf8
		pos = f.pushByte(0xf8, pos)
		pos = f.pushDelay(2, pos)
		return pos
	}

	// Program Data & PC++: 0xe0
	pos = f.pushByte(0xe0, pos)
	pos = f.pushDelay(2, pos)

	value15_8_1 := (uint32(value8) << 1) // 0:Start bit, 8:value, 0:Stop bit
	pos = f.pushByte(byte((value15_8_1>>16)&0xff), pos)
	pos = f.pushByte(byte((value15_8_1>>8)&0xff), pos)
	pos = f.pushByte(byte((value15_8_1>>0)&0xff), pos)

	// T PDFM: 11[msec]
	pos = f.pushDelayMillisecond(11, pos)

	return pos
}

func (f *Flash) loadAddress(addr uint32) error {
	b := 0
	e := 0

	// Load PC address: 0x80
	e = f.pushByte(0x80, e)
	e = f.pushDelay(2, e)

	addr1_22_1 := ((addr & 0x3f_ffff) << 1) // 0:Start bit, 22:addr, 0:Stop bit
	e = f.pushByte(byte((addr1_22_1>>16)&0xff), e)
	e = f.pushByte(byte((addr1_22_1>>8)&0xff), e)
	e = f.pushByte(byte((addr1_22_1>>0)&0xff), e)
	e = f.pushDelay(2, e)

	_, err := f.devA.write(f.commands[b:e])
	if err != nil {
		return err
	}

	return nil
}

func (f *Flash) read64Words() ([]byte, error) {
	b := 0
	e := 0

	for i := 0; i < 64; i++ {
		e = f.pushReadWord(e)
	}

	_, err := f.devA.write(f.commands[b:e])
	if err != nil {
		return nil, err
	}

	results24 := f.commands[0 : 24*64]
	err = f.devA.readAll(results24)
	if err != nil {
		return nil, err
	}

	values := make([]byte, 64*2)
	for i := 0; i < 64; i++ {
		// from MSB
		value16 := uint16(0)
		for _, b := range results24[24*i+7 : 24*i+7+16] {
			value16 = ((value16 << 1) | uint16((b&0b0010_0000)>>5))
		}
		// swap
		values[i*2+0] = byte(value16 & 0xff)
		values[i*2+1] = byte(value16 >> 8)
	}
	return values, nil
}

func (f *Flash) readWord() (value16 [2]byte, err error) {
	b := 0
	e := 0

	e = f.pushReadWord(e)

	_, err = f.devA.write(f.commands[b:e])
	if err != nil {
		return value16, err
	}

	result24 := f.commands[e : e+24]
	err = f.devA.readAll(result24)
	if err != nil {
		return value16, err
	}

	// from MSB
	var u16 uint16
	for _, b := range result24[7 : 7+16] {
		u16 = ((u16 << 1) | uint16((b&0b0010_0000)>>5))
	}
	// swap
	value16[0] = byte(u16 & 0xff)
	value16[1] = byte(u16 >> 8)

	return value16, nil
}

func (f *Flash) readByte() (byte, error) {
	value16, err := f.readWord()
	if err != nil {
		return 0, err
	}
	return byte(value16[0]), nil
}
