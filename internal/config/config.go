// Package config는 환경변수에서 에이전트 실행 설정을 읽어 온다.
//
// 컨테이너 1개 = 차량 1대로 띄우기 때문에, 차량마다 달라지는 값(차량 ID 등)은
// 이미지에 넣지 않고 docker-compose 등에서 환경변수로 주입한다.
// 항목별 의미와 기본값은 docs/vehicle-agent.md 3장에 정리되어 있다.
package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// 환경변수를 지정하지 않았을 때 쓰는 기본값.
// const 블록은 컴파일 시점에 값이 정해지는 상수를 묶어서 선언한다.
// 이름이 소문자로 시작하므로 이 패키지 안에서만 쓸 수 있다(비공개).
const (
	defaultCheckinInterval = 30 * time.Second // time.Duration 타입. 30 * 1초
	defaultDataDir         = "./data"
	defaultInitialVersion  = "1.0.0"
)

// Config는 에이전트 실행 설정이다.
//
// 이름이 대문자로 시작하는 타입·필드·함수는 다른 패키지에서 쓸 수 있다(공개).
// Go에는 public/private 키워드가 없고, 이름의 첫 글자로 공개 여부가 정해진다.
type Config struct {
	// VehicleID는 차량 고유 ID다. 서버 경로와 상태 파일의 차량을 식별한다.
	VehicleID string

	// VehicleModel은 서버가 캠페인 대상을 판정할 때 쓰는 차종이다.
	VehicleModel string

	// HWVersion은 서버가 캠페인 대상을 판정할 때 쓰는 TCU 하드웨어 리비전이다.
	HWVersion string

	// Region은 서버가 캠페인 대상을 판정할 때 선택적으로 쓰는 차량 지역이다.
	Region string

	// EnrollmentKey는 기동 시 차량 등록 API에만 보내는 공통 등록 키다.
	EnrollmentKey string

	// ServerURL은 OTA 서버의 베이스 URL이다. 예: "http://ota-server:8080"
	// 끝의 "/"는 제거해 두므로, 쓰는 쪽에서 ServerURL + "/api/v1/..."처럼
	// 바로 이어 붙여도 "//"가 생기지 않는다.
	ServerURL string

	// CheckinInterval은 서버에 업데이트가 있는지 물어보는(체크인) 주기다.
	CheckinInterval time.Duration

	// DataDir은 상태 파일과 다운로드 파일을 저장할 디렉터리다.
	// 컨테이너에서는 볼륨을 붙여서, 컨테이너가 재시작돼도 내용이 남게 한다.
	DataDir string

	// InitialVersion은 최초 기동 시(상태 파일이 아직 없을 때) 현재 버전으로 쓸 값이다.
	// 공장에서 출고될 때 설치되어 있던 펌웨어 버전이라고 생각하면 된다.
	InitialVersion string

	// ManifestPublicKey는 Base64 환경변수에서 복호화한 Ed25519 공개키다.
	// 매니페스트가 제공한 SHA-256 해시의 서명을 검증할 때 쓴다.
	ManifestPublicKey ed25519.PublicKey
}

// Load는 환경변수를 읽어 Config를 만들고, 값이 올바른지 검증한다.
//
// getenv는 "키를 받아 값을 돌려주는 함수"다. 실제 실행에서는 os.Getenv를 넘기고,
// 테스트에서는 map으로 만든 가짜 함수를 넘긴다. 이렇게 함수를 인자로 받으면
// 테스트가 실제 환경변수를 건드리지 않아도 된다.
//
// Go는 예외(exception) 대신 에러를 반환값으로 돌려준다.
// 성공하면 (cfg, nil), 실패하면 (빈 Config, 에러)를 반환한다.
// 잘못된 항목이 여러 개면 하나씩 고치며 재실행하지 않도록 한 번에 모두 알려준다.
func Load(getenv func(string) string) (Config, error) {
	// 필수값이 아니고 따로 검증할 것도 없는 항목은 바로 채운다.
	// 체크인 주기는 아래에서 파싱에 성공했을 때만 덮어쓰므로 일단 기본값으로 둔다.
	cfg := Config{
		VehicleID:       getenv("VEHICLE_ID"),
		VehicleModel:    getenv("VEHICLE_MODEL"),
		HWVersion:       getenv("HW_VERSION"),
		Region:          getenv("REGION"),
		EnrollmentKey:   getenv("ENROLLMENT_KEY"),
		CheckinInterval: defaultCheckinInterval,
		DataDir:         valueOr(getenv("DATA_DIR"), defaultDataDir),
		InitialVersion:  valueOr(getenv("INITIAL_VERSION"), defaultInitialVersion),
	}

	// 발견한 에러를 모아 두는 슬라이스(가변 길이 배열).
	// var로 선언만 하면 nil 슬라이스가 되며, append로 바로 추가할 수 있다.
	var errs []error

	// 1) VEHICLE_ID: 필수값 + API 계약의 문자·길이 제한
	if err := validateVehicleID(cfg.VehicleID); err != nil {
		errs = append(errs, fmt.Errorf("VEHICLE_ID: %w", err))
	}

	// 2) 차량 속성과 등록 키는 등록·체크인 요청에 반드시 필요하다.
	if cfg.VehicleModel == "" {
		errs = append(errs, errors.New("VEHICLE_MODEL is required"))
	}
	if cfg.HWVersion == "" {
		errs = append(errs, errors.New("HW_VERSION is required"))
	}
	if cfg.EnrollmentKey == "" {
		errs = append(errs, errors.New("ENROLLMENT_KEY is required"))
	}

	// 3) OTA_SERVER_URL: 필수값 + URL 형식 검사
	serverURL, err := parseServerURL(getenv("OTA_SERVER_URL"))
	if err != nil {
		// %w는 원래 에러를 감싸서(wrap) 보존한다. 앞에 붙인 "OTA_SERVER_URL: "이
		// 어떤 항목에서 난 에러인지 알려주는 맥락이 된다.
		errs = append(errs, fmt.Errorf("OTA_SERVER_URL: %w", err))
	}
	cfg.ServerURL = serverURL

	// 4) CHECKIN_INTERVAL: 선택값. 지정했을 때만 파싱한다.
	// if 문 안에서 변수를 선언하고(raw := ...) 조건을 검사하는 Go 문법이다.
	// 여기서 선언한 raw는 이 if 블록 안에서만 쓸 수 있다.
	if raw := getenv("CHECKIN_INTERVAL"); raw != "" {
		// ParseDuration은 "30s", "1m", "1h30m" 같은 문자열을 time.Duration으로 바꾼다.
		// 단위가 없는 "30"은 에러다.
		d, err := time.ParseDuration(raw)

		// 조건 없는 switch는 if-else if 체인을 읽기 좋게 쓴 것이다.
		// 위에서부터 처음으로 참인 case 하나만 실행된다(break 불필요).
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("CHECKIN_INTERVAL: %w", err))
		case d <= 0:
			// 0이나 음수 주기는 체크인이 끝없이 반복되거나 동작하지 않으므로 막는다.
			errs = append(errs, fmt.Errorf("CHECKIN_INTERVAL: must be positive, got %s", d))
		default:
			cfg.CheckinInterval = d
		}
	}

	// 5) MANIFEST_PUBLIC_KEY: Base64로 전달받아 Ed25519 공개키 길이를 확인한다.
	// 문자열을 그대로 두지 않으면 검증기가 매번 파싱할 필요가 없고,
	// 기동 시 잘못된 키를 즉시 발견할 수 있다.
	publicKey, err := parseManifestPublicKey(getenv("MANIFEST_PUBLIC_KEY"))
	if err != nil {
		errs = append(errs, fmt.Errorf("MANIFEST_PUBLIC_KEY: %w", err))
	} else {
		cfg.ManifestPublicKey = publicKey
	}

	if len(errs) > 0 {
		// errors.Join은 여러 에러를 하나로 합친다. 출력하면 줄바꿈으로 구분된다.
		// 실패했을 때는 일부만 채워진 설정을 쓰지 못하도록 빈 Config를 반환한다.
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

// validateVehicleID는 API 계약의 vehicleId 제한을 확인한다.
// 영문·숫자·하이픈만 허용해 URL 경로와 서버 저장소에서 같은 ID를 안전하게 사용한다.
func validateVehicleID(vehicleID string) error {
	if vehicleID == "" {
		return errors.New("is required")
	}
	if len(vehicleID) > 64 {
		return fmt.Errorf("must be at most 64 characters, got %d", len(vehicleID))
	}
	for _, c := range vehicleID {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-') {
			return fmt.Errorf("must contain only letters, digits, or hyphens, got %q", vehicleID)
		}
	}
	return nil
}

// parseManifestPublicKey는 Base64 환경변수에서 Ed25519 공개키를 만든다.
// Ed25519 공개키는 항상 32바이트여야 하므로, 다른 길이는 서명 검증 전에 거부한다.
func parseManifestPublicKey(raw string) (ed25519.PublicKey, error) {
	if raw == "" {
		return nil, errors.New("is required")
	}

	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("must be standard Base64: %w", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("must decode to %d bytes, got %d", ed25519.PublicKeySize, len(decoded))
	}
	return ed25519.PublicKey(decoded), nil
}

// parseServerURL은 서버 URL이 비어 있지 않고, http/https 스킴과 호스트를 갖췄는지 검사한다.
// 통과하면 끝의 "/"를 제거한 URL을 반환한다.
func parseServerURL(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("is required")
	}

	// url.Parse는 형식이 느슨해서 "ota-server:8080" 같은 값도 에러 없이 통과시킨다.
	// 그래서 스킴과 호스트를 따로 확인한다.
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		// %q는 값을 따옴표로 감싸 출력한다. 빈 문자열이나 공백도 눈에 보이게 된다.
		return "", fmt.Errorf("scheme must be http or https, got %q", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("host is missing in %q", raw)
	}

	return strings.TrimRight(raw, "/"), nil
}

// valueOr는 v가 비어 있으면 fallback을, 아니면 v를 반환한다.
// 환경변수가 없을 때 기본값을 쓰기 위한 작은 도우미 함수다.
func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
