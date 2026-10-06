package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// 상태 파일 이름. 위치는 설정의 DATA_DIR 아래다.
const (
	fileName    = "state.json"
	tmpFileName = "state.json.tmp" // 원자적 저장을 위한 임시 파일
)

// ErrNotFound는 상태 파일이 아직 없다는 뜻이다(최초 기동).
//
// 패키지 수준에 에러 값을 만들어 두면, 호출하는 쪽에서
// errors.Is(err, state.ErrNotFound)로 "파일이 없는 경우"만 골라 처리할 수 있다.
// Go에서는 이런 값을 센티널(sentinel) 에러라고 부른다.
var ErrNotFound = errors.New("state file not found")

// Store는 상태를 JSON 파일로 저장하고 읽는다.
//
// 필드가 소문자(dir)라서 패키지 밖에서는 직접 바꿀 수 없다.
// 반드시 NewStore로 만들어 쓰게 해서 잘못된 값으로 생성되는 것을 막는다.
type Store struct {
	dir string
}

// NewStore는 dir 디렉터리에 상태 파일을 저장하는 Store를 만든다.
//
// *Store(포인터)를 반환하는 것은 Go에서 흔한 생성자 패턴이다.
// Go에는 클래스 생성자 문법이 없어서 New~ 이름의 함수가 그 역할을 한다.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Load는 상태 파일을 읽는다. 파일이 없으면 ErrNotFound를 반환한다.
//
// (s *Store)는 포인터 리시버다. 메서드 안에서 구조체를 복사하지 않고 원본을 쓴다.
// 한 타입의 메서드는 리시버 종류(값/포인터)를 한쪽으로 통일하는 것이 관례다.
func (s *Store) Load() (State, error) {
	raw, err := os.ReadFile(s.path())
	if err != nil {
		// 파일이 없는 경우만 ErrNotFound로 바꿔 준다.
		// 권한 문제 같은 다른 에러는 그대로 감싸서 올려 보낸다.
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, ErrNotFound
		}
		return State{}, fmt.Errorf("read state file: %w", err)
	}

	var st State
	// json.Unmarshal은 JSON 바이트를 구조체로 바꾼다.
	// 결과를 채워 넣어야 하므로 st의 주소(&st)를 넘긴다.
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("parse state file %s: %w", s.path(), err)
	}
	if err := st.Validate(); err != nil {
		return State{}, fmt.Errorf("invalid state file %s: %w", s.path(), err)
	}
	return st, nil
}

// LoadOrInit은 저장된 상태를 읽고, 없으면 초기 상태를 만들어 저장한 뒤 반환한다.
// 에이전트 기동 시 한 번 호출한다.
//
//   - 최초 기동(파일 없음): initialVersion으로 대기 상태를 만들어 저장한다.
//   - 재시작(파일 있음): 저장된 상태를 그대로 반환한다. initialVersion은 무시한다.
//   - 파일의 vehicle_id가 실행 중인 vehicleID와 다르면 에러를 반환한다. 다른 차량의 볼륨을 잘못 붙인 경우다.
func (s *Store) LoadOrInit(vehicleID, initialVersion string) (State, error) {
	st, err := s.Load()
	switch {
	case errors.Is(err, ErrNotFound):
		st = New(vehicleID, initialVersion)
		if err := s.Save(st); err != nil {
			return State{}, fmt.Errorf("save initial state: %w", err)
		}
		return st, nil
	case err != nil:
		return State{}, err
	}

	if st.VehicleID != vehicleID {
		return State{}, fmt.Errorf("state file belongs to vehicle ID %q, but this agent is %q", st.VehicleID, vehicleID)
	}
	return st, nil
}

// Save는 상태를 파일에 원자적으로 저장한다.
//
// 원자적 저장이란 "완전히 저장되었거나, 전혀 바뀌지 않았거나" 둘 중 하나만 일어나는 것이다.
// state.json에 바로 덮어쓰다가 중간에 죽으면(전원 차단) 반쯤 쓰인 깨진 파일이 남는다.
// 그래서 임시 파일에 끝까지 쓴 뒤 rename으로 한 번에 바꿔치기한다.
// 같은 디렉터리 안의 rename은 운영체제가 원자적으로 처리한다.
func (s *Store) Save(st State) error {
	// 규칙에 어긋난 상태는 아예 디스크에 남기지 않는다.
	if err := st.Validate(); err != nil {
		return fmt.Errorf("refuse to save invalid state: %w", err)
	}

	// MarshalIndent는 사람이 읽기 좋게 들여쓰기한 JSON을 만든다.
	// 상태 파일을 직접 열어 보며 디버깅할 수 있도록 한 줄 JSON 대신 이 방식을 쓴다.
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}

	// 0o755는 8진수 권한 표기다(소유자 rwx, 나머지 r-x). 디렉터리가 이미 있으면 아무 일도 안 한다.
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	tmpPath := filepath.Join(s.dir, tmpFileName)
	if err := writeFileSync(tmpPath, raw); err != nil {
		return err
	}

	// 임시 파일이 완전히 기록된 뒤에만 실제 파일 이름으로 바꾼다.
	if err := os.Rename(tmpPath, s.path()); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return syncDir(s.dir)
}

// path는 상태 파일의 전체 경로를 반환한다.
// filepath.Join은 운영체제에 맞는 구분자(/ 또는 \)로 경로를 이어 붙인다.
func (s *Store) path() string {
	return filepath.Join(s.dir, fileName)
}

// writeFileSync는 파일에 data를 쓰고, 디스크에 실제로 기록될 때까지 기다린다.
//
// 운영체제는 성능을 위해 쓰기를 메모리에 잠시 모아 두었다가 나중에 디스크에 쓴다.
// 그 사이에 전원이 나가면 "썼다고 생각한" 내용이 사라질 수 있어서 Sync로 강제로 기록한다.
func writeFileSync(path string, data []byte) (err error) {
	// O_TRUNC: 이전 임시 파일이 남아 있으면 내용을 비우고 새로 쓴다.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open temp state file: %w", err)
	}

	// defer는 이 함수가 끝날 때(return 직후) 실행할 코드를 예약한다.
	// 중간에 에러로 빠져나가도 파일이 반드시 닫힌다.
	//
	// 함수 시그니처에서 반환값에 이름(err)을 붙였기 때문에,
	// defer 안에서 Close 에러를 반환값에 반영할 수 있다.
	// 앞에서 이미 에러가 났다면(err != nil) 그 에러를 우선한다.
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close temp state file: %w", cerr)
		}
	}()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync temp state file: %w", err)
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
		return fmt.Errorf("open data dir: %w", err)
	}
	defer d.Close() // 읽기 전용으로 연 디렉터리라 Close 에러는 무시해도 된다

	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync data dir: %w", err)
	}
	return nil
}
