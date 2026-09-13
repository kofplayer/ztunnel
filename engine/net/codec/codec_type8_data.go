package netCodec

import (
	"fmt"
)

func NewCodec_type8_data() Codec {
	return new(codec_type8_data)
}

type codec_type8_data struct {
}

func (this *codec_type8_data) Encode(cb uint32, t uint32, v []byte) ([]byte, error) {
	// msgID 由 1 字节承载。原先 `uint8(t)` 会把 >255 的值**静默截断**：
	// 调用方以为发的是 300，对端解出来是 44 —— 这是不会报错的协议损坏。
	// 扩展协议前必须让它失败得看得见（报告 L-2）。
	if t > 0xFF {
		return nil, fmt.Errorf("codec_type8: msgID %d exceeds 1-byte header (max 255)", t)
	}
	tData := make([]byte, 1, len(v)+1)
	tData[0] = uint8(t)
	data := append(tData, v...)
	return data, nil
}

func (this *codec_type8_data) Decode(data []byte) (uint32, uint32, []byte, error) {
	n := len(data)
	if n < 1 {
		return 0, 0, nil, fmt.Errorf("data len is %v, less than 1", n)
	}
	t := uint32(data[0])
	return uint32(0), t, data[1:], nil
}
