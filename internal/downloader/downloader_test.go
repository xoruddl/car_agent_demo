package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// firmware는 테스트용 가짜 펌웨어 내용이다. 실제로는 수 MB의 바이너리지만
// 다운로드·해시 로직은 크기와 상관없으므로 짧은 바이트로 충분하다. (반복 횟수만 다름)
var firmware = []byte("fake firmware 1.1.0")

// sha256Hex는 data의 SHA256을 소문자 hex 문자열로 돌려준다(매니페스트의 sha256 형식).
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data) // [32]byte 배열을 돌려준다
	return hex.EncodeToString(sum[:])
}

// newCDN은 body를 status 코드로 돌려주는 가짜 CDN 서버를 띄운다.
// 테스트가 끝나면 t.Cleanup으로 자동으로 내려간다.
func newCDN(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 정상 응답이고 해시가 맞으면, dest에 받은 내용이 그대로 저장된다.
// dest의 상위 디렉터리(downloads/)가 없어도 만들어서 저장해야 한다.
func TestDownload_Success(t *testing.T) {
	srv := newCDN(t, http.StatusOK, firmware)
	// t.TempDir()는 테스트마다 새 임시 디렉터리를 만들고, 끝나면 자동으로 지운다.
	dest := filepath.Join(t.TempDir(), "downloads", "cmp-42.bin")

	d := New(5 * time.Second)
	if err := d.Download(context.Background(), srv.URL+"/fw/1.1.0.bin", dest, sha256Hex(firmware)); err != nil {
		t.Fatalf("Download: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(firmware) {
		t.Errorf("dest content = %q, want %q", got, firmware)
	}
}

// 재시작으로 이전 다운로드의 부분 파일이 남아 있어도, 처음부터 다시 받아 덮어써야 한다.
// 기존 파일이 새 내용보다 길 때 뒷부분이 남으면 해시가 틀어지므로 긴 쓰레기 값으로 확인한다.
func TestDownload_OverwritesPartialFile(t *testing.T) {
	// 가짜 CDN 서버
	// 200 OK, firmware 19 바이트 응답
	srv := newCDN(t, http.StatusOK, firmware)

	// 저장 경로
	dest := filepath.Join(t.TempDir(), "cmp-42.bin")

	// 받다 만 파일의 내용 흉내
	leftover := []byte("partial data from a previous run that is longer than firmware")
	if err := os.WriteFile(dest, leftover, 0o644); err != nil {
		t.Fatalf("write leftover: %v", err)
	}

	d := New(5 * time.Second)
	// 가짜 CDN 에서 파일을 받아 dest 에 저장
	// sha256Hex(firmware): 기대 해시
	if err := d.Download(context.Background(), srv.URL, dest, sha256Hex(firmware)); err != nil {
		t.Fatalf("Download: %v", err)
	}

	// 옛 내용이 섞여있으면 해시가 틀려서 여기서 실패함
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(firmware) {
		t.Errorf("dest content = %q, want %q", got, firmware)
	}
}

// 받은 내용의 해시가 매니페스트와 다르면 ErrHashMismatch를 돌려주고 파일을 지운다.
// 에이전트는 errors.Is로 이 에러를 구분해 HASH_MISMATCH 코드를 기록한다.
func TestDownload_HashMismatch(t *testing.T) {
	srv := newCDN(t, http.StatusOK, []byte("tampered firmware"))
	dest := filepath.Join(t.TempDir(), "cmp-42.bin")

	d := New(5 * time.Second)
	err := d.Download(context.Background(), srv.URL, dest, sha256Hex(firmware))
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("err = %v, want ErrHashMismatch", err)
	}
	assertNotExist(t, dest)
}

// 네트워크·HTTP 쪽 실패는 ErrHashMismatch가 아닌 에러여야 한다(→ DOWNLOAD_FAILED).
// 실패하면 받다 만 파일을 남기지 않는다.
//
// 테이블 테스트: 입력과 기대값만 다른 비슷한 테스트를 구조체 슬라이스로 모아 반복 실행하는 Go 관례.
func TestDownload_Failures(t *testing.T) {
	tests := []struct {
		name string
		url  func(t *testing.T) string // 테스트마다 요청할 URL을 만든다
	}{
		{
			name: "non-200 status",
			url: func(t *testing.T) string {
				return newCDN(t, http.StatusNotFound, []byte("not found")).URL
			},
		},
		{
			name: "connection refused",
			url: func(t *testing.T) string {
				// 서버를 띄웠다가 바로 닫아서, 아무도 듣지 않는 주소를 얻는다.
				srv := httptest.NewServer(http.NotFoundHandler())
				srv.Close()
				return srv.URL
			},
		},
	}

	for _, tt := range tests {
		// t.Run은 하위 테스트를 만든다. 실패 시 어떤 케이스인지 이름으로 표시된다.
		t.Run(tt.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "cmp-42.bin")

			d := New(5 * time.Second)
			err := d.Download(context.Background(), tt.url(t), dest, sha256Hex(firmware))
			if err == nil {
				t.Fatal("err = nil, want error")
			}
			if errors.Is(err, ErrHashMismatch) {
				t.Errorf("err = %v, must not be ErrHashMismatch", err)
			}
			assertNotExist(t, dest)
		})
	}
}

// ctx가 취소되면 다운로드를 멈추고 에러를 돌려준다.
// 에이전트가 종료 신호(SIGTERM)를 받았을 때 다운로드가 끝날 때까지 기다리지 않게 하기 위해서다.
func TestDownload_ContextCanceled(t *testing.T) {
	srv := newCDN(t, http.StatusOK, firmware)
	dest := filepath.Join(t.TempDir(), "cmp-42.bin")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 시작하기 전에 미리 취소해 둔다

	d := New(5 * time.Second)
	err := d.Download(ctx, srv.URL, dest, sha256Hex(firmware))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	assertNotExist(t, dest)
}

// assertNotExist는 path에 파일이 없는지 확인한다.
func assertNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s should not exist, stat err = %v", path, err)
	}
}
