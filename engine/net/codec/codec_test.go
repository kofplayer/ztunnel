package netCodec

import (
	"testing"

	"ztunnel/testutil"
)

func TestCodecData_Identity(t *testing.T) {
	c := NewCodec_data()
	msg := []byte{1, 2, 3, 0xFF}
	out, err := c.Encode(0, 0, msg)
	testutil.NoError(t, err)
	testutil.BytesEqual(t, msg, out)

	cb, id, got, err := c.Decode(out)
	testutil.NoError(t, err)
	testutil.Equal(t, uint32(0), cb)
	testutil.Equal(t, uint32(0), id)
	testutil.BytesEqual(t, msg, got)
}

func TestCodecType8_Roundtrip(t *testing.T) {
	c := NewCodec_type8_data()
	out, err := c.Encode(0, 3, []byte("ab"))
	testutil.NoError(t, err)
	testutil.BytesEqual(t, []byte{3, 'a', 'b'}, out)

	cb, id, got, err := c.Decode(out)
	testutil.NoError(t, err)
	testutil.Equal(t, uint32(0), cb)
	testutil.Equal(t, uint32(3), id)
	testutil.BytesEqual(t, []byte("ab"), got)
}

func TestCodecType8_DecodeTooShort(t *testing.T) {
	_, _, _, err := NewCodec_type8_data().Decode(nil)
	testutil.Error(t, err)
}

// 回归 L-2：type8 的 msgID 由 1 字节承载，原先 `uint8(t)` 会**静默截断**——
// 调用方以为发的是 300，对端解出来是 44，属于不会报错的协议损坏。
// （本用例原先固化的是"截断即现状"，修复后改为断言明确拒绝。）
func TestCodecType8_MsgIDTooWide_IsRejected(t *testing.T) {
	c := NewCodec_type8_data()

	// 边界内仍可正常往返
	out, err := c.Encode(0, 255, []byte("x"))
	testutil.NoError(t, err, "msgID=255 应合法")
	_, id, got, err := c.Decode(out)
	testutil.NoError(t, err)
	testutil.Equal(t, uint32(255), id)
	testutil.BytesEqual(t, []byte("x"), got)

	// 越界必须报错且不产出任何字节
	_, err = c.Encode(0, 256, nil)
	testutil.Error(t, err, "L-2 未修复：msgID=256 被静默截断")
	_, err = c.Encode(0, 300, []byte("abc"))
	testutil.Error(t, err, "L-2 未修复：msgID=300 被静默截断")
	_, err = c.Encode(0, 1<<32-1, nil)
	testutil.Error(t, err, "L-2 未修复：极大 msgID 被静默截断")
}

// 另两个 codec 的同族边界。
func TestCodecOtherWidths_Rejected(t *testing.T) {
	c16 := NewCodec_type16_data()
	if _, err := c16.Encode(0, 65535, nil); err != nil {
		t.Fatalf("msgID=65535 应合法: %v", err)
	}
	if _, err := c16.Encode(0, 65536, nil); err == nil {
		t.Fatal("L-2 未修复：type16 对 msgID=65536 静默截断")
	}

	cb8 := NewCodec_cb8_type16_data()
	if _, err := cb8.Encode(255, 65535, nil); err != nil {
		t.Fatalf("cb=255/msgID=65535 应合法: %v", err)
	}
	if _, err := cb8.Encode(256, 0, nil); err == nil {
		t.Fatal("L-2 未修复：cb=256 被 uint8 静默截断")
	}
	if _, err := cb8.Encode(0, 65536, nil); err == nil {
		t.Fatal("L-2 未修复：msgID=65536 被 uint16 静默截断")
	}
}

func TestCodecType16_Roundtrip(t *testing.T) {
	c := NewCodec_type16_data()
	out, err := c.Encode(0, 65535, []byte("x"))
	testutil.NoError(t, err)
	_, id, got, err := c.Decode(out)
	testutil.NoError(t, err)
	testutil.Equal(t, uint32(65535), id)
	testutil.BytesEqual(t, []byte("x"), got)
}

func TestCodecType16_DecodeTooShort(t *testing.T) {
	_, _, _, err := NewCodec_type16_data().Decode([]byte{1})
	testutil.Error(t, err)
}

func TestCodecCB8Type16_Roundtrip(t *testing.T) {
	c := NewCodec_cb8_type16_data()
	out, err := c.Encode(7, 300, []byte("y"))
	testutil.NoError(t, err)
	cb, id, got, err := c.Decode(out)
	testutil.NoError(t, err)
	testutil.Equal(t, uint32(7), cb)
	testutil.Equal(t, uint32(300), id)
	testutil.BytesEqual(t, []byte("y"), got)
}

func TestCodecCB8Type16_DecodeTooShort(t *testing.T) {
	_, _, _, err := NewCodec_cb8_type16_data().Decode([]byte{1, 2})
	testutil.Error(t, err)
}
