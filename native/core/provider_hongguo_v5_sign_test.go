package core

import (
	"encoding/binary"
	"testing"
)

// 交叉校验：V5 helios 用的 ladonEncrypt 必须与线上验证过的 hongguoPlayerLadon（同 license 1611921764）逐字节一致。
// 这是 V5 签名移植的回归守护：任一侧 ladon 实现漂移都会在此暴露。
func TestHongguoV5LadonCrossCheck(t *testing.T) {
	for i := 0; i < 2000; i++ {
		ts := uint32(1700000000 + i*37)
		var prefix [4]byte
		binary.LittleEndian.PutUint32(prefix[:], uint32(i*2654435761))
		aid := "8662"
		if i%2 == 0 {
			aid = "1967"
		}
		want := hongguoPlayerLadon(aid, ts, prefix[:])
		aidInt := int64(8662)
		if aid == "1967" {
			aidInt = 1967
		}
		got := ladonEncrypt(int64(ts), 1611921764, aidInt, prefix[:])
		if want != got {
			t.Fatalf("ladon 不一致 i=%d ts=%d aid=%s\n want=%s\n got =%s", i, ts, aid, want, got)
		}
	}
}
