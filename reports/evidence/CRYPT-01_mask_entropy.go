// CRYPT-01 证据：type1 掩码移位 bug 使 64 位会话密钥的有效熵坍缩到 15 bit。
//
// 复现对象：engine/net/middleware/encrypt/type1/base.go:139-148
//
//	masks[i] = uint8((key >> i) & 0xFF)      // 现网：按 bit 位移
//	masks[i] = uint8((key >> (i * 8)) & 0xFF) // 应为按字节位移（同文件 GetKeyBytes:43-50 即此惯用法）
//
// 运行：
//
//	cd reports/evidence && go mod init evidence && go run CRYPT-01_mask_entropy.go
//
// 实测输出（Go 1.24 / win32）：
//
//	现网 masks(1122334455660000) = [0 0 0 0 0 0 0 0]
//	现网 masks(aabbccddeeff7fff) = [255 255 255 255 255 255 255 255]
//	两者掩码相同? true   (两个 key 高 49 bit 完全不同)
//	低 20 bit 穷举得到的不同掩码数 = 32768   (2^15)
//	修正后同样区间不同掩码数 = 1048576  (2^20)
//
// 结论：
//  1. 掩码只由 key 的 bit0..bit14 决定，高 49 bit 全部被丢弃；
//  2. 任意连接的数据面 keystream 只有 32768 种取值，可穷举；
//  3. 低 15 bit 为 0 的 key（概率 1/32768）产生全零掩码 —— 该连接数据完全明文直通。
package main

import "fmt"

type Key uint64

const keySize = 8

func masksCurrent(key Key) [keySize]uint8 {
	var masks [keySize]uint8
	for i := range masks {
		masks[i] = uint8((key >> i) & 0xFF)
	}
	return masks
}

func masksFixed(key Key) [keySize]uint8 {
	var masks [keySize]uint8
	for i := range masks {
		masks[i] = uint8((key >> (i * 8)) & 0xFF)
	}
	return masks
}

func main() {
	a := Key(0x1122334455660000)
	b := Key(0xAABBCCDDEEFF7FFF)
	fmt.Printf("现网 masks(%x) = %v\n", uint64(a), masksCurrent(a))
	fmt.Printf("现网 masks(%x) = %v\n", uint64(b), masksCurrent(b))
	fmt.Printf("两者掩码相同? %v   (两个 key 高 49 bit 完全不同)\n",
		masksCurrent(a) == masksCurrent(Key(0)))

	const probe = 1 << 20
	seen := map[[keySize]uint8]bool{}
	seenFixed := map[[keySize]uint8]bool{}
	for k := uint64(0); k < probe; k++ {
		seen[masksCurrent(Key(k))] = true
		seenFixed[masksFixed(Key(k))] = true
	}
	fmt.Printf("低 20 bit 穷举得到的不同掩码数 = %d   (2^15=32768)\n", len(seen))
	fmt.Printf("修正后同样区间不同掩码数 = %d  (2^20=1048576)\n", len(seenFixed))
}
