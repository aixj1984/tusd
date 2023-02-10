package uid

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"time"
)

// uid returns a unique id. These ids consist of 128 bits from a
// cryptographically strong pseudo-random generator and are like uuids, but
// without the dashes and significant bits.
//
// See: http://en.wikipedia.org/wiki/UUID#Random_UUID_probability_of_duplicates
func Uid() string {
	id := make([]byte, 20)
	_, err := io.ReadFull(rand.Reader, id)
	if err != nil {
		// This is probably an appropriate way to handle errors from our source
		// for random bits.
		panic(err)
	}
	//return hex.EncodeToString(id)
	// add time tag into uid
	srcId := []byte(hex.EncodeToString(id))
	currentDate := time.Now().Format("2006010215")
	for i := 0; i < 10; i++ {
		srcId[(i*4)+1] = currentDate[i]
	}
	return string(srcId)
}
