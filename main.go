package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ysh86/ftPIC/d2xx"

	"github.com/marcinbor85/gohex"
)

func main() {
	//Mandelbrot()

	// args
	var (
		outFile string
		inFile  string
	)
	flag.StringVar(&outFile, "r", "", "read whole internal flash")
	flag.StringVar(&inFile, "w", "", "an ihex file to write")
	flag.Parse()
	if len(flag.Args()) > 0 {
		flag.Usage()
		return
	}

	// ft
	verMajor, verMinor, verPatch := d2xx.Version()
	fmt.Printf("d2xx library version: %d.%d.%d\n", verMajor, verMinor, verPatch)

	flash, err := d2xx.OpenFlash()
	if err != nil {
		fmt.Fprintf(os.Stderr, "d2xx: %s\n", err)
		return
	}
	defer flash.Close()

	devType, venID, devID := flash.DevInfo()
	fmt.Printf("DevType: %v(%d), vendor ID: 0x%04x, device ID: 0x%04x\n", devType, devType, venID, devID)

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
