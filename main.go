package main

import (
	"bufio"
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

	if ihexFile != "" {
		//Mandelbrot()
		binFile := ihexFile + ".bin"
		dumpBin(binFile, ihexFile)
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
		err := writeFlash(flash, inFile)
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

	n, err = io.Copy(w, flash)
	if err != nil {
		return 0, err
	}

	return n, err
}

func writeFlash(flash *d2xx.Flash, inFile string) error {
	r, err := os.Open(inFile)
	if err != nil {
		return err
	}
	defer r.Close()

	ihex := gohex.NewMemory()
	err = ihex.ParseIntelHex(r)
	if err != nil {
		return err
	}

	for _, segment := range ihex.GetDataSegments() {
		fmt.Println()
		fmt.Println("segment:")

		b := int(segment.Address)
		e := b + len(segment.Data)
		data := segment.Data[0:]

		for b < e {
			i := 0

			fmt.Printf("%06x:", b)
			for i < 16 && b < e {
				fmt.Printf(" %02x", data[i])
				i++
				b++
			}
			fmt.Println()

			data = data[i:]
		}
	}

	return nil
}

func dumpBin(binFile, ihexFile string) error {
	fw, err := os.Create(binFile)
	if err != nil {
		return err
	}
	defer fw.Close()
	w := bufio.NewWriter(fw)
	defer w.Flush()

	r, err := os.Open(ihexFile)
	if err != nil {
		return err
	}
	defer r.Close()

	ihex := gohex.NewMemory()
	err = ihex.ParseIntelHex(r)
	if err != nil {
		return err
	}

	i := 0
	for _, segment := range ihex.GetDataSegments() {
		b := int(segment.Address)
		e := b + len(segment.Data)
		data := segment.Data[0:]

		fmt.Printf("segment: %06x-%06x\n", b, e)

		for i < b {
			w.WriteByte(0xff)
			i++
		}

		w.Write(data)
		i += len(data)
	}

	return nil
}
