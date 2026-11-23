package core

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// 字节级回归：对齐 iDuanJu STVTests/HGCurrentPlayerTests.swift 的合成向量
// （testdata/hongguo/current_crypto.json / current_factor.json），确保移植的
// compute / transform / factor 与 Swift 参考实现逐字节一致。
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestHongguoCurrentVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/hongguo/current_crypto.json")
	if err != nil {
		t.Skipf("缺少向量文件：%v", err)
	}
	var vectors struct {
		Whole []struct {
			ID     string `json:"id"`
			Inputs struct {
				RawQuery           string `json:"raw_query"`
				RecordDigest       []byte `json:"record_digest"`
				FactorPB           []byte `json:"factor_pb"`
				Material           []byte `json:"material"`
				LicenseID          string `json:"license_id"`
				Aid                string `json:"aid"`
				Seconds            uint64 `json:"seconds"`
				GorgonNonce        uint16 `json:"gorgon_nonce"`
				NativeGorgonFlags  uint16 `json:"native_gorgon_flags"`
				HeliosRandom       uint32 `json:"helios_random"`
				MedusaRandom1      uint32 `json:"medusa_random1"`
				MedusaRandom2      uint32 `json:"medusa_random2"`
				LocalBufferAddress uint64 `json:"local_buffer_address"`
				PreviousThread     uint32 `json:"previous_thread_state"`
			} `json:"inputs"`
			ExpectedHeaders map[string]string `json:"expected_header_sha256"`
			ExpectedState   uint32            `json:"expected_thread_state"`
		} `json:"whole"`
		Transforms []struct {
			Branch   int    `json:"branch"`
			Key      []byte `json:"key"`
			IV       []byte `json:"iv"`
			Plain    []byte `json:"plain"`
			Expected string `json:"expected_sha256"`
		} `json:"transforms"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("解析 current_crypto.json 失败：%v", err)
	}
	if len(vectors.Whole) == 0 || len(vectors.Transforms) == 0 {
		t.Fatalf("向量为空（whole=%d transforms=%d）", len(vectors.Whole), len(vectors.Transforms))
	}

	for i, v := range vectors.Transforms {
		out, err := hongguoCurrentTransform(v.Plain, v.Key, v.IV, v.Branch)
		if err != nil {
			t.Fatalf("transform[%d] 失败：%v", i, err)
		}
		if sha256Hex(out) != v.Expected {
			t.Fatalf("transform[%d] branch=%d 不匹配", i, v.Branch)
		}
	}

	for _, v := range vectors.Whole {
		x := v.Inputs
		headers, err := hongguoCurrentCompute(
			x.RawQuery, x.RecordDigest, x.FactorPB, x.Material, x.LicenseID, x.Aid, x.Seconds,
			x.GorgonNonce, x.NativeGorgonFlags, x.HeliosRandom, x.MedusaRandom1, x.MedusaRandom2,
			x.LocalBufferAddress, x.PreviousThread,
		)
		if err != nil {
			t.Fatalf("%s compute 失败：%v", v.ID, err)
		}
		for name, expected := range v.ExpectedHeaders {
			got := headers[name]
			if got == "" {
				t.Fatalf("%s 缺少 header %s", v.ID, name)
			}
			if sha256Hex([]byte(got)) != expected {
				t.Fatalf("%s header %s 不匹配：got=%q", v.ID, name, got)
			}
		}
		// 校验 state：x-ladon 是 base64(BE32(state))，反解比对。
		gotState, err := hongguoDecodeLadonState(headers["x-ladon"])
		if err != nil {
			t.Fatalf("%s 解析 x-ladon 失败：%v", v.ID, err)
		}
		if gotState != v.ExpectedState {
			t.Fatalf("%s state 不匹配：got=%d want=%d", v.ID, gotState, v.ExpectedState)
		}
	}
}

func hongguoDecodeLadonState(ladon string) (uint32, error) {
	raw, err := base64.StdEncoding.DecodeString(ladon)
	if err != nil {
		return 0, err
	}
	if len(raw) != 4 {
		return 0, errLadonBadLen
	}
	return binary.BigEndian.Uint32(raw), nil
}

var errLadonBadLen = errors.New("x-ladon 长度异常")

func TestHongguoCurrentFactorVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/hongguo/current_factor.json")
	if err != nil {
		t.Skipf("缺少向量文件：%v", err)
	}
	var factors []struct {
		ToySeed         int    `json:"toy_seed"`
		PublicInputSeed int    `json:"public_input_seed"`
		PublicSeconds   uint32 `json:"public_seconds"`
		OutputSHA256    string `json:"output_sha256"`
	}
	if err := json.Unmarshal(raw, &factors); err != nil {
		t.Fatalf("解析 current_factor.json 失败：%v", err)
	}
	if len(factors) == 0 {
		t.Fatal("factor 向量为空")
	}
	for i, f := range factors {
		input := make([]byte, 52)
		for j := range input {
			input[j] = byte(j*37 + f.PublicInputSeed)
		}
		out, err := hongguoCurrentFactor(input, f.PublicSeconds, f.ToySeed)
		if err != nil {
			t.Fatalf("factor[%d] 失败：%v", i, err)
		}
		if sha256Hex(out) != f.OutputSHA256 {
			t.Fatalf("factor[%d] 不匹配（seed=%d）", i, f.ToySeed)
		}
	}
}
