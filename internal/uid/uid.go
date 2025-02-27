package uid

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5"
)

// uid returns a unique id. These ids consist of 128 bits from a
// cryptographically strong pseudo-random generator and are like uuids, but
// without the dashes and significant bits.
//
// See: http://en.wikipedia.org/wiki/UUID#Random_UUID_probability_of_duplicates
/*
func Uid() string {
	id := make([]byte, 20)
	_, err := io.ReadFull(rand.Reader, id)
	if err != nil {
		// This is probably an appropriate way to handle errors from our source
		// for random bits.
		panic(err)
	}
	// return hex.EncodeToString(id)
	// add time tag into uid
	srcId := []byte(hex.EncodeToString(id))
	currentDate := time.Now().Format("2006010215")
	for i := 0; i < 10; i++ {
		srcId[(i*4)+1] = currentDate[i]
	}
	return string(srcId)
}
*/
// Uid 生成40位的唯一ID, UUID 32位，每4位新增一个随机值，扩展到40位，再每4位中第2位用时间更新
func Uid() string {
	u2, err := uuid.NewV4()
	if err != nil {
		panic(err)
	}
	// 去掉 UUID 中的 `-`
	rawUUID := strings.ReplaceAll(u2.String(), "-", "")

	// 取前 32 个字符（UUID 去掉 `-` 后正好 32 字符）
	rawUUID = rawUUID[:32]

	// 用于存储最终的 40 字符 ID
	var customID []byte

	// 生成 8 个随机字符（用于插入到 UUID）
	randBytes := make([]byte, 8)
	_, err = rand.Read(randBytes)
	if err != nil {
		panic("failed to generate random bytes")
	}
	randStr := hex.EncodeToString(randBytes)[:8] // 8 个字符

	// 时间格式化字符串（10 位）
	currentDate := time.Now().Format("2006010215")

	// 构造 40 位字符串，每 4 位插入一个随机字符，并用时间替换部分字符
	for i := 0; i < 8; i++ {
		// 添加 UUID 的 4 个字符
		customID = append(customID, rawUUID[i*4:i*4+4]...)

		// 插入随机字符
		customID = append(customID, randStr[i])
	}

	// 替换 customID 中的部分字符为时间字符
	for i := 0; i < 10; i++ {
		// 替换每 4 组中的第 2 个字符
		customID[i*4+1] = currentDate[i]
	}

	return string(customID)
}
