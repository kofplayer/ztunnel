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

// 固化现状：type8 用 uint8 承载 msgID，大于 255 时静默截断。
// 当前协议 msgID ≤ 4 无影响，但扩展协议前必须处理（报告 #17 杂项）。
func TestCodecType8_MsgIDTruncation_CurrentBehavior(t *testing.T) {
	c := NewCodec_type8_data()
	out, err := c.Encode(0, 300, nil)
	testutil.NoError(t, err)
	_, id, _, err := c.Decode(out)
	testutil.NoError(t, err)
	testutil.Equal(t, uint32(300&0xFF), id)
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
