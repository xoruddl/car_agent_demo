package installer

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile은 테스트용 파일을 만든다. 상위 디렉터리가 없으면 함께 만든다.
// t.Helper()를 부르면 실패 메시지에 이 함수가 아니라 호출한 테스트의 줄 번호가 찍힌다.
func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readFile은 path의 내용을 문자열로 읽는다. 읽지 못하면 테스트를 멈춘다.
func readFile(t *testing.T, path string) string {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(got)
}

// assertNoTmp는 설치 후 임시 파일이 남아 있지 않은지 확인한다.
func assertNoTmp(t *testing.T, inst *Installer) {
	t.Helper()
	// os.Stat이 IsNotExist 에러를 돌려주면 파일이 없다는 뜻이다.
	if _, err := os.Stat(filepath.Join(inst.dir, tmpFileName)); !os.IsNotExist(err) {
		t.Errorf("temp file should not remain, stat err = %v", err)
	}
}

// 설치에 성공하는 경우를 테이블 테스트로 묶는다.
// 테이블 테스트: 입력과 기대값을 구조체 슬라이스로 나열하고, 같은 검증 코드를 반복 실행하는 Go 관례다.
func TestInstall_Success(t *testing.T) {
	tests := []struct {
		name     string
		existing string // 설치 전에 이미 있던 current.bin 내용. 빈 문자열이면 파일 없음(최초 설치)
		times    int    // Install을 몇 번 호출할지. 2 이상이면 재시작 후 재설치(멱등성)를 흉내 낸다
	}{
		{name: "최초 설치", existing: "", times: 1},
		{name: "기존 펌웨어 덮어쓰기", existing: "old firmware 1.0.0", times: 1},
		{name: "같은 입력으로 다시 설치해도 결과가 같다", existing: "", times: 2},
	}

	for _, tt := range tests {
		// t.Run은 하위 테스트를 만든다. 실패하면 어느 케이스인지 이름으로 바로 알 수 있다.
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			src := filepath.Join(dataDir, "downloads", "cmp-42.bin")
			const firmware = "fake firmware 1.1.0"
			writeFile(t, src, []byte(firmware))

			inst := New(dataDir)
			if tt.existing != "" {
				writeFile(t, inst.CurrentPath(), []byte(tt.existing))
			}

			for n := 0; n < tt.times; n++ {
				if err := inst.Install(src); err != nil {
					t.Fatalf("Install #%d: %v", n+1, err)
				}
			}

			if got := readFile(t, inst.CurrentPath()); got != firmware {
				t.Errorf("current.bin = %q, want %q", got, firmware)
			}
			// 원본은 지우지 않는다. 재시작 후 재설치와 결과 보고 뒤 정리를 위해 남겨 둔다.
			if got := readFile(t, src); got != firmware {
				t.Errorf("source = %q, want unchanged %q", got, firmware)
			}
			assertNoTmp(t, inst)
		})
	}
}

// 원본 파일이 없으면 에러를 내고, 기존에 설치된 펌웨어는 그대로 남아야 한다.
// 설치 실패가 차량을 펌웨어 없는 상태로 만들면 안 되기 때문이다.
func TestInstall_MissingSource(t *testing.T) {
	dataDir := t.TempDir()
	inst := New(dataDir)
	const old = "old firmware 1.0.0"
	writeFile(t, inst.CurrentPath(), []byte(old))

	err := inst.Install(filepath.Join(dataDir, "downloads", "missing.bin"))
	if err == nil {
		t.Fatal("Install: want error, got nil")
	}

	if got := readFile(t, inst.CurrentPath()); got != old {
		t.Errorf("current.bin = %q, want unchanged %q", got, old)
	}
	assertNoTmp(t, inst)
}
