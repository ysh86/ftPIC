//go:build cgo

package d2xx

/*
#cgo CFLAGS: -I${SRCDIR}/native
#cgo LDFLAGS: -L${SRCDIR}/native/linux_amd64 -lftd2xx
*/
import "C"
