package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ysh86/ftPIC/d2xx"

	"github.com/marcinbor85/gohex"
)

func main() {
	// args
	var (
		outFile  string
		inFile   string
		ihexFile string
	)
	flag.StringVar(&outFile, "r", "", "read whole internal flash")
	flag.StringVar(&inFile, "w", "", "an ihex file to write")
	flag.StringVar(&ihexFile, "i", "", "dump ihex to raw bin")
	flag.Parse()
	if len(flag.Args()) > 0 {
		flag.Usage()
		return
	}

	// load
	var err error
	var data []byte
	if ihexFile != "" {
		inFile = ihexFile
	}
	if inFile != "" {
		fmt.Println("Load info:")
		data, err = loadHex(inFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "loadHex: %s\n", err)
			return
		}
		fmt.Println()
	}

	// load only
	if ihexFile != "" {
		//Mandelbrot()
		binFile := ihexFile + ".bin"
		err := os.WriteFile(binFile, data, 0666)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load: %s\n", err)
		}
		return
	}

	flash, err := d2xx.OpenFlash()
	if err != nil {
		fmt.Fprintf(os.Stderr, "d2xx: %s\n", err)
		return
	}
	defer flash.Close()

	// ft (writer)
	verMajor, verMinor, verPatch := d2xx.Version()
	devType, venID, devID := flash.WriterInfo()
	fmt.Println("Writer info:")
	fmt.Printf("d2xx library version: %d.%d.%d\n", verMajor, verMinor, verPatch)
	fmt.Printf("DevType: %v(%d), vendor ID: 0x%04x, device ID: 0x%04x\n", devType, devType, venID, devID)
	fmt.Println()

	// target
	fmt.Println("Target info:")
	fmt.Printf("device: %04X, revision: %04X (%s%d)\n",
		flash.DeviceID,
		flash.RevisionID,
		flash.RevisionMajor,
		flash.RevisionMinor,
	)
	fmt.Printf("User IDs (32 Words)\n")
	for i := 0; i < 32; i += 8 {
		for ii := 0; ii < 8; ii++ {
			value16 := flash.UserIDs[i+ii]
			fmt.Printf(" %02x %02x", value16[0], value16[1])
		}
		fmt.Println()
	}
	fmt.Printf("Configuration Bytes (10 Bytes)\n")
	for i := 0; i < 10; i++ {
		fmt.Printf(" %02x", flash.Configuration[i])
	}
	fmt.Println()
	fmt.Println()

	// dump
	if outFile != "" {
		n, err := dumpFlash(flash, outFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dump: %s\n", err)
		} else {
			fmt.Printf("dump: %d [bytes]\n", n)
		}
	}

	// write ihex
	if inFile != "" {
		err := writeFlash(flash, data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "write: %s\n", err)
		} else {
			fmt.Println("write: done")
		}
	}
}

func dumpFlash(flash *d2xx.Flash, outFile string) (n int64, err error) {
	w, err := os.Create(outFile)
	if err != nil {
		return 0, err
	}
	defer w.Close()

	_, err = flash.Seek(0, io.SeekStart)
	if err != nil {
		return 0, err
	}

	n, err = io.Copy(w, flash)
	if err != nil {
		return 0, err
	}

	return n, err
}

func writeFlash(flash *d2xx.Flash, data []byte) error {
	err := flash.BulkErase(d2xx.REGION_FLASH)
	if err != nil {
		return err
	}
	_, err = flash.Seek(0, io.SeekStart)
	if err != nil {
		return err
	}
	return flash.WritePFM(data)
}

func loadHex(ihexFile string) ([]byte, error) {
	r, err := os.Open(ihexFile)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	ihex := gohex.NewMemory()
	err = ihex.ParseIntelHex(r)
	if err != nil {
		return nil, err
	}

	var w bytes.Buffer
	i := 0
	for _, segment := range ihex.GetDataSegments() {
		b := int(segment.Address)
		e := b + len(segment.Data)
		data := segment.Data[0:]

		fmt.Printf("segment: %06x-%06x: %7d [bytes]\n", b, e, e-b)

		for i < b {
			w.WriteByte(0xff)
			i++
		}

		w.Write(data)
		i += len(data)
	}

	return w.Bytes(), nil
}
