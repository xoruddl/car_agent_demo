package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testPending은 여러 테스트에서 쓰는 진행 중 업데이트 예시를 만든다.
func testPending() *PendingUpdate {
	return &PendingUpdate{
		CampaignID:    "cmp-42",
		TargetVersion: "1.1.0",
		URL:           "http://cdn/fw/1.1.0.bin",
		SHA256:        "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
	}
}

// 저장한 상태를 다시 읽으면 똑같은 값이 나와야 한다(왕복 테스트).
func TestStore_SaveAndLoad(t *testing.T) {
	// t.TempDir()은 테스트 전용 임시 디렉터리를 만들고, 테스트가 끝나면 자동으로 지운다.
	store := NewStore(t.TempDir())

	saved := State{
		VIN:            "V1",
		CurrentVersion: "1.0.0",
		Status:         Downloading,
		PendingUpdate:  testPending(),
	}
	if err := store.Save(saved); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// PendingUpdate는 포인터라서 ==로 비교하면 주소가 달라 항상 다르다고 나온다.
	// 그래서 포인터가 가리키는 값(*)끼리 따로 비교한다.
	if loaded.VIN != saved.VIN || loaded.CurrentVersion != saved.CurrentVersion ||
		loaded.Status != saved.Status || loaded.ErrorCode != saved.ErrorCode {
		t.Errorf("Load() = %+v, want %+v", loaded, saved)
	}
	if loaded.PendingUpdate == nil || *loaded.PendingUpdate != *saved.PendingUpdate {
		t.Errorf("Load().PendingUpdate = %+v, want %+v", loaded.PendingUpdate, saved.PendingUpdate)
	}
}

// 상태 파일의 JSON 필드 이름이 docs/vehicle-agent.md에 적은 형식과 같은지 확인한다.
// 필드 이름을 실수로 바꾸면, 이미 저장된 파일을 읽지 못하게 되므로 테스트로 고정한다.
func TestStore_FileFormat(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.Save(New("V1", "1.0.0")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, want := range []string{
		`"vin": "V1"`,
		`"current_version": "1.0.0"`,
		`"state": "IDLE"`,
		`"pending_update": null`,
		`"error_code": ""`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("state.json에 %s 가 없음:\n%s", want, raw)
		}
	}
}

// 저장이 끝나면 임시 파일이 남아 있지 않아야 한다(rename으로 교체되었으므로).
func TestStore_SaveLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.Save(New("V1", "1.0.0")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// os.Stat이 fs.ErrNotExist를 돌려주면 파일이 없다는 뜻이다.
	if _, err := os.Stat(filepath.Join(dir, "state.json.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("임시 파일이 남아 있음 (Stat error = %v)", err)
	}
}

// 규칙에 어긋난 상태는 디스크에 쓰지 않아야 한다.
func TestStore_SaveRejectsInvalidState(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	invalid := State{VIN: "V1", CurrentVersion: "1.0.0", Status: Downloading} // PendingUpdate 없음
	if err := store.Save(invalid); err == nil {
		t.Fatal("Save() error = nil, want error")
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("잘못된 상태인데 파일이 만들어짐 (Stat error = %v)", err)
	}
}

// 상태 파일이 없으면 ErrNotFound를 돌려줘야 한다.
func TestStore_LoadNotFound(t *testing.T) {
	_, err := NewStore(t.TempDir()).Load()

	// errors.Is는 감싸진(wrap) 에러 안쪽까지 확인해서 같은 에러인지 판단한다.
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Load() error = %v, want ErrNotFound", err)
	}
}

// 상태 파일이 깨져 있으면 에러를 내야 한다(잘못된 상태로 동작하지 않도록).
func TestStore_LoadCorrupted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := NewStore(dir).Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("깨진 파일인데 ErrNotFound로 처리됨: %v", err)
	}
}

// 최초 기동: 파일이 없으면 초기 버전으로 대기 상태를 만들고 디스크에도 저장해야 한다.
func TestStore_LoadOrInit_FirstBoot(t *testing.T) {
	store := NewStore(t.TempDir())

	st, err := store.LoadOrInit("V1", "1.0.0")
	if err != nil {
		t.Fatalf("LoadOrInit() error = %v", err)
	}
	if want := New("V1", "1.0.0"); st != want {
		t.Errorf("LoadOrInit() = %+v, want %+v", st, want)
	}

	// 저장까지 되었는지 다시 읽어서 확인한다.
	if _, err := store.Load(); err != nil {
		t.Errorf("최초 상태가 저장되지 않음: %v", err)
	}
}

// 재시작: 파일이 있으면 초기 버전을 무시하고 저장된 상태를 그대로 이어가야 한다.
func TestStore_LoadOrInit_Restart(t *testing.T) {
	store := NewStore(t.TempDir())

	// 1.1.0으로 업데이트하고 결과 보고 중에 죽은 상황을 흉내 낸다.
	before := State{VIN: "V1", CurrentVersion: "1.1.0", Status: Reporting, PendingUpdate: testPending()}
	if err := store.Save(before); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	st, err := store.LoadOrInit("V1", "1.0.0")
	if err != nil {
		t.Fatalf("LoadOrInit() error = %v", err)
	}
	if st.CurrentVersion != "1.1.0" || st.Status != Reporting {
		t.Errorf("LoadOrInit() = %+v, 저장된 상태(1.1.0, REPORTING)를 이어가야 함", st)
	}
}

// 다른 차량의 상태 파일(볼륨)을 잘못 붙이면 기동을 거부해야 한다.그럼
func TestStore_LoadOrInit_VINMismatch(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(New("OTHER-VIN", "1.0.0")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	_, err := store.LoadOrInit("V1", "1.0.0")
	if err == nil {
		t.Fatal("LoadOrInit() error = nil, want VIN 불일치 에러")
	}
	if !strings.Contains(err.Error(), "OTHER-VIN") {
		t.Errorf("에러 메시지에 저장된 VIN이 없음: %v", err)
	}
}
