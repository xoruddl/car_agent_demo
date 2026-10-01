// Package installer는 검증된 펌웨어 파일을 설치 위치에 반영하는 설치 과정을 흉내 낸다.
//
// 실제 차량이라면 ECU에 펌웨어를 플래싱하겠지만, 이 시뮬레이션에서는
// 다운로드 파일을 ${DATA_DIR}/firmware/current.bin으로 복사하는 것을 "설치"로 본다.
//
// 설치기는 파일만 다룬다. current_version 교체, 상태 저장, INSTALL_FAILED 기록은
// 호출하는 쪽(agent)이 맡는다. 설치기가 상태 파일까지 건드리면 책임이 섞이기 때문이다.
package installer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	firmwareDir     = "firmware"        // ${DATA_DIR} 아래 설치 디렉터리
	currentFileName = "current.bin"     // 설치된 펌웨어 파일 이름
	tmpFileName     = "current.bin.tmp" // 원자적 교체를 위한 임시 파일
)

// Installer는 펌웨어 파일을 설치 위치에 반영한다.
type Installer struct {
	dir string // 설치 디렉터리(${DATA_DIR}/firmware)
}

// New는 dataDir 아래 firmware/ 디렉터리에 설치하는 Installer를 만든다.
// 디렉터리는 여기서 만들지 않고 Install할 때 필요하면 만든다.
func New(dataDir string) *Installer {
	return &Installer{dir: filepath.Join(dataDir, firmwareDir)}
}

// CurrentPath는 설치된 펌웨어 파일의 전체 경로를 반환한다.
// 테스트나 로그에서 설치 결과를 확인할 때 쓴다.
func (i *Installer) CurrentPath() string {
	return filepath.Join(i.dir, currentFileName)
}

// Install은 src 파일(다운로드·검증을 마친 펌웨어)을 firmware/current.bin에 반영한다.
//
// 원본을 rename으로 옮기지 않고 "복사 → rename" 순서로 처리하는 이유:
//   - 멱등성: INSTALLING 상태에서 프로세스가 죽으면 재시작 후 Install을 다시 호출한다.
//     원본을 옮겨 버렸다면 두 번째 호출 때 src가 없어 실패한다. 복사하면 몇 번을 다시 실행해도 결과가 같다.
//   - 원자성: 임시 파일에 다 쓴 뒤 rename으로 바꾸므로, 설치 도중 죽어도
//     반쯤 쓰인 current.bin이 남지 않는다. 기존 펌웨어 아니면 새 펌웨어, 둘 중 하나만 보인다.
//
// src는 지우지 않는다. 다운로드 파일 정리는 결과 보고까지 마친 뒤 agent가 한다.
//
// 반환값에 이름(err)을 붙인 "named return"이라, 아래 defer에서 최종 에러를 보고 정리 여부를 정할 수 있다.
func (i *Installer) Install(src string) (err error) {
	// 1. 원본 열기. 가장 먼저 해야 원본이 없을 때 firmware/를 건드리지 않고 끝난다.
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source firmware: %w", err)
	}
	defer in.Close() // 함수가 끝날 때 닫는다. 읽기 전용이라 Close 에러는 무시해도 된다

	// 2. 설치 디렉터리 준비. MkdirAll은 mkdir -p처럼 이미 있으면 아무 일도 하지 않는다.
	if err := os.MkdirAll(i.dir, 0o755); err != nil {
		return fmt.Errorf("create firmware dir: %w", err)
	}

	// 임시 파일은 current.bin과 같은 디렉터리에 둔다. 같은 파일시스템 안이어야 rename이 원자적이다.
	tmpPath := filepath.Join(i.dir, tmpFileName)

	// 실패로 끝나면 복사하다 만 임시 파일을 지운다.
	// 클로저가 바깥의 err를 참조하므로 함수가 끝나는 시점의 최종 값을 본다.
	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath) // 임시 파일이 생기기 전 실패면 지울 게 없으니 에러 무시
		}
	}()

	// 3. 임시 파일에 복사. 이 동안 current.bin은 그대로라, 여기서 죽어도 기존 펌웨어는 멀쩡하다.
	if err := copyFileSync(tmpPath, in); err != nil {
		return err
	}

	// 4. 이름 바꾸기로 교체. rename은 내용 복사 없이 이름만 바꾸며 원자적이다.
	// 밖에서 보면 current.bin은 기존 내용 아니면 새 내용일 뿐, 반쯤 바뀐 상태는 보이지 않는다.
	if err := os.Rename(tmpPath, i.CurrentPath()); err != nil {
		return fmt.Errorf("replace current firmware: %w", err)
	}

	// 5. 디렉터리도 Sync해야 전원이 나가도 rename 결과가 유지된다.
	return syncDir(i.dir)
}

// copyFileSync는 r의 내용을 path 파일에 스트리밍으로 쓰고, 디스크에 기록될 때까지 기다린다.
//
// 펌웨어는 수 MB 이상일 수 있으므로 io.Copy로 조각씩 옮긴다(파일 전체를 메모리에 올리지 않는다).
// Sync로 강제로 기록하는 이유: agent는 Install이 성공하면 current_version을 바꿔 저장하는데,
// 그 직후 전원이 나가도 펌웨어 내용이 디스크에 남아 있어야 상태와 실제 파일이 어긋나지 않는다.
func copyFileSync(path string, r io.Reader) (err error) {
	// O_TRUNC: 이전 실행에서 남은 임시 파일이 있으면 비우고 처음부터 새로 쓴다.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open temp firmware file: %w", err)
	}
	// 쓰기용 파일은 Close에서도 에러가 날 수 있다. 앞에서 이미 에러가 났다면 그 에러를 우선한다.
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close temp firmware file: %w", cerr)
		}
	}()

	// io.Copy는 32KB씩 읽고 쓰기를 반복하므로 펌웨어 크기와 상관없이 메모리를 적게 쓴다.
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("copy firmware: %w", err)
	}
	// Write만으로는 OS 버퍼에 머물 수 있어서, Sync로 디스크에 실제 기록될 때까지 기다린다.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync temp firmware file: %w", err)
	}
	return nil
}

// syncDir은 디렉터리 정보(어떤 이름이 어떤 파일을 가리키는지)를 디스크에 기록한다.
//
// rename은 "파일 내용"이 아니라 "디렉터리 항목"을 바꾸는 작업이다.
// 디렉터리도 Sync해 두어야 전원이 나가도 rename 결과가 유지된다.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open firmware dir: %w", err)
	}
	defer d.Close() // 읽기 전용으로 연 디렉터리라 Close 에러는 무시해도 된다

	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync firmware dir: %w", err)
	}
	return nil
}
