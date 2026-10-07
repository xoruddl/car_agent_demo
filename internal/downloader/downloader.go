// Package downloader는 CDN에서 펌웨어를 내려받으면서 SHA256을 검증한다(③).
//
// 펌웨어는 수 MB 이상이 될 수 있으므로 파일 전체를 메모리에 올리지 않는다.
// 네트워크에서 읽은 조각을 파일 쓰기와 해시 계산에 동시에 흘려보내는 스트리밍 방식으로 처리한다.
// 요청 형식은 docs/api.md의 ③ 참고.
package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ErrHashMismatch는 받은 파일의 SHA256이 매니페스트 값과 다르다는 에러다.
//
// 이렇게 미리 만들어 둔 에러 값을 "sentinel error"라고 부른다.
// 호출하는 쪽(agent)은 errors.Is(err, ErrHashMismatch)로 이 경우를 골라내
// HASH_MISMATCH 코드를 기록하고, 그 밖의 에러는 DOWNLOAD_FAILED로 처리한다.
// downloader가 state 패키지의 실패 코드를 직접 알 필요가 없도록 에러 값으로만 구분해 준다.
var ErrHashMismatch = errors.New("sha256 mismatch")

// ErrSizeMismatch는 받은 파일 크기가 매니페스트의 fileSize와 다를 때 반환한다.
var ErrSizeMismatch = errors.New("file size mismatch")

// ErrURLRejected는 CDN이 서명 URL을 거절했을 때(403 서명 불일치, 410 만료) 반환한다.
//
// 펌웨어 문제가 아니라 URL이 낡았다는 뜻이므로 업데이트 실패로 기록하면 안 된다.
// 에이전트는 errors.Is로 이 에러를 골라내 매니페스트를 다시 요청해 새 URL을 받는다.
// (예: 차량이 꺼져 있다 다음 날 켜지면 저장해 둔 URL은 이미 만료돼 있다.)
var ErrURLRejected = errors.New("download url rejected")

// Downloader는 HTTP GET으로 펌웨어를 받아 파일로 저장한다.
type Downloader struct {
	http *http.Client // 실제 HTTP 요청을 보내는 표준 라이브러리 클라이언트
}

// New는 Downloader를 만든다.
//
// timeout은 요청 하나가 연결부터 본문을 다 받을 때까지 걸릴 수 있는 최대 시간이다.
// 본문 수신 시간까지 포함되므로, 펌웨어 크기와 네트워크 속도를 고려해 넉넉히 잡아야 한다.
// 타임아웃이 없으면 CDN이 응답을 멈췄을 때 에이전트가 DOWNLOADING에서 영원히 멈춰 있게 된다.
func New(timeout time.Duration) *Downloader {
	return &Downloader{
		http: &http.Client{Timeout: timeout},
	}
}

// Download는 url의 펌웨어를 dest 파일에 저장하고, 내용의 SHA256이 wantSHA256과 같은지 확인한다.
//
//   - dest의 상위 디렉터리가 없으면 만든다.
//   - dest에 이전 실행의 부분 파일이 있으면 처음부터 다시 받아 덮어쓴다(이어받기는 범위 밖).
//   - CDN이 URL을 거절하면(403, 410) ErrURLRejected를 감싼 에러를 돌려준다.
//   - 실패하면(네트워크 오류, 200이 아닌 응답, 크기·해시 불일치, ctx 취소) dest를 지운다.
//     검증되지 않은 파일이 남아 설치 단계로 넘어가는 일을 막기 위해서다.
//
// 반환값에 이름(err)을 붙인 "named return"을 쓴다. 이렇게 하면 아래 defer 안에서
// 함수가 최종적으로 돌려줄 에러를 보고 성공·실패에 따라 정리 작업을 다르게 할 수 있다.
func (d *Downloader) Download(ctx context.Context, url, dest string, wantSize int64, wantSHA256 string) (err error) {
	// 0o755: 소유자는 읽기·쓰기·실행, 나머지는 읽기·실행 권한. 0o는 8진수 표기다.
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}

	// 어떤 경로로 실패하든 받다 만 파일을 지운다. 파일이 아직 없으면 Remove가 에러를 내지만 무시한다.
	defer func() {
		if err != nil {
			_ = os.Remove(dest)
		}
	}()

	// NewRequestWithContext로 만들어야 ctx가 취소될 때 다운로드도 함께 멈춘다.
	// 요청 만들기, *http.Reqeust 구조체만 만듦
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	// 요청 보내기
	// Do 는 응답헤더까지만 받고 반환, 본문은 네트워크에 남아있고, resp.Body를 읽을 때 조금씩 흘러들어옴 (스트리밍 처리)
	resp, err := d.http.Do(req)
	if err != nil {
		// 연결 실패, 타임아웃, ctx 취소 등. %w로 감싸 두면 errors.Is(err, context.Canceled)로 확인할 수 있다.
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	// docs/api.md 4장: 403·410은 매니페스트 재요청 대상이고, 그 밖의 200이 아닌 응답은 DOWNLOAD_FAILED.
	// 206 같은 다른 2xx도 기대하지 않으므로 200만 성공으로 본다.
	// switch는 위에서부터 처음 맞는 case 하나만 실행한다(다른 언어와 달리 break가 필요 없다).
	switch resp.StatusCode {
	case http.StatusOK:
		// 정상 응답. 아래에서 본문을 받는다.
	case http.StatusForbidden, http.StatusGone:
		return fmt.Errorf("%w: status %d", ErrURLRejected, resp.StatusCode)
	default:
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	// os.Create는 파일이 없으면 만들고, 있으면 길이를 0으로 잘라(truncate) 비운 뒤 연다.
	// 그래서 이전 실행의 부분 파일이 남아 있어도 처음부터 새로 쓰게 된다.
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	// 서버에서 받은 펌웨어를 파일로 저장하면서, 동시에 그 내용의 SHA256 지문을 계산한다.
	// sha256.New()는 데이터를 조금씩 넣으면서 해시를 누적 계산하는 io.Writer를 돌려준다.
	// io.MultiWriter는 한 번 쓴 데이터를 여러 Writer(파일, 해시)에 똑같이 나눠 쓴다.
	// io.Copy는 resp.Body에서 32KB씩 읽어 이 Writer에 쓰기를 반복하므로,
	// 펌웨어 크기와 상관없이 메모리는 버퍼 크기만큼만 쓴다.
	// 1. 다운로드와 저장: resp.Body(서버 응답)에서 데이터를 끝까지 읽어 f(디스크의 dest 파일)에 쓴다
	// 2. 해시 계산: 같은 데이터를 hasher에도 넣어서 SHA256을 누적 계산
	// 3. 실패 처리: 도중에 문제가 생기면(연결 끊김, 취소, 타임아웃, 디스크 가득 참) 파일을 닫고 에러를 반환합니다. 받다 만 파일은 앞서 등록한 defer가 삭제
	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, hasher), resp.Body)
	if err != nil {
		f.Close()
		return fmt.Errorf("download body: %w", err)
	}

	// Sync는 OS 버퍼에 있는 내용을 디스크에 실제로 기록한다.
	// 에이전트는 이 함수가 성공하면 INSTALLING 상태를 저장하는데, 그 직후 전원이 꺼져도
	// 파일 내용이 디스크에 남아 있어야 재시작 후 설치를 이어갈 수 있다.
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync file: %w", err)
	}
	// 쓰기용 파일은 Close에서도 에러가 날 수 있으므로(디스크 가득 참 등) 확인한다.
	if err := f.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}
	if size != wantSize {
		return fmt.Errorf("%w: got %d, want %d", ErrSizeMismatch, size, wantSize)
	}

	// Sum(nil)은 지금까지 넣은 데이터의 해시를 []byte로 돌려준다. 매니페스트와 같은 소문자 hex로 바꿔 비교한다.
	got := hex.EncodeToString(hasher.Sum(nil))
	if got != wantSHA256 {
		// %w로 ErrHashMismatch를 감싸면, 메시지에 실제 값을 덧붙여도 errors.Is로 구분할 수 있다.
		return fmt.Errorf("%w: got %s, want %s", ErrHashMismatch, got, wantSHA256)
	}
	return nil
}
