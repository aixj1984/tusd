package uid

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid/v5"
)

func TestEventHandler(t *testing.T) {
	// Create a Version 4 UUID.
	u2, err := uuid.NewV4()
	if err != nil {
		fmt.Printf("failed to generate UUID: %v\n", err)
	}
	fmt.Printf("generated Version 4 UUID %v\n", u2.String())

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

	fmt.Printf("generated Version 4 UUID %v\n", string(customID))
}
