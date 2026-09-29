# 차량 에이전트 설계

## 1. 역할

실제 차량의 TCU(통신 모듈) + OTA 클라이언트를 흉내 내는 독립 프로세스.
스스로 주기적으로 서버에 체크인하고, 업데이트가 있으면 내려받아 검증·설치한 뒤 결과를 보고한다.

```
빌드·서명 ──▶ 원본 저장소(S3) ──▶ CDN
    │                              │
    │ 메타데이터 등록                 │ ③ 다운로드
    ▼                              ▼
 OTA 서버 ◀── ① 체크인 ──────── 차량 에이전트 (이 레포)
          ── ② 매니페스트 ──▶
          ◀── ④ 결과 보고 ───
```

에이전트가 직접 통신하는 대상은 **OTA 서버(①②④)**와 **CDN(③)** 두 곳뿐이다.
프로토콜 상세는 [api.md](api.md)를 본다.

## 2. 현재 범위 (최소 버전)

| 포함 | 제외 (필요해지면 추가) |
|---|---|
| HTTP polling 체크인 | MQTT 푸시 |
| 단순 GET 다운로드 | Range 이어받기, 속도 제한 |
| SHA256 검증 | 매니페스트 서명(ed25519) |
| 단일 설치 위치 | A/B 슬롯, 롤백 |
| 실패 시 다음 주기에 재시도 | 지수 백오프 + jitter |
| — | 주행·배터리 조건, 차종·지역별 타기팅 |

## 3. 설정 (환경변수)

| 변수 | 필수 | 기본값 | 설명 |
|---|---|---|---|
| `VIN` | O | — | 차량 고유 ID |
| `OTA_SERVER_URL` | O | — | OTA 서버 베이스 URL (예: `http://ota-server:8080`) |
| `CHECKIN_INTERVAL` | | `30s` | 체크인 주기 (Go duration 형식) |
| `DATA_DIR` | | `./data` | 상태·다운로드 파일 저장 위치. 컨테이너에서는 볼륨(`/data`) |
| `INITIAL_VERSION` | | `1.0.0` | 상태 파일이 없을 때(최초 기동) 사용할 현재 버전 |

## 4. 상태 모델

로컬 파일 `${DATA_DIR}/state.json`에 저장하며, 재시작 시 이 파일로 복구한다.

```json
{
  "vin": "KMHEV6000000001",
  "current_version": "1.0.0",
  "state": "DOWNLOADING",
  "pending_update": {
    "campaign_id": "cmp-42",
    "target_version": "1.1.0",
    "url": "http://cdn/fw/1.1.0.bin",
    "sha256": "ab12..."
  },
  "error_code": ""
}
```

| 필드 | 설명 |
|---|---|
| `vin` | 차량 ID. 설정의 `VIN`과 다르면 기동을 거부한다(다른 차량의 볼륨을 잘못 붙인 경우). |
| `current_version` | 현재 설치된 펌웨어 버전 |
| `state` | 상태 머신의 현재 상태 |
| `pending_update` | 진행 중인 업데이트. `IDLE`이면 `null` |
| `error_code` | 진행 중 발생한 실패 코드. 성공이면 빈 문자열 |

## 5. 상태 머신

```
        ① 업데이트 있음
IDLE ─────────────────▶ DOWNLOADING ──③ + 해시 OK──▶ INSTALLING
 ▲                          │                           │
 │                          │ 실패 (error_code 기록)      │ 성공 / 실패
 │                          ▼                           ▼
 └──────── ④ 보고 성공 ──── REPORTING ◀──────────────────┘
```

| 상태 | 하는 일 | 다음 상태 |
|---|---|---|
| `IDLE` | ① 체크인. 업데이트가 있으면 `pending_update` 저장 | 업데이트 있음 → `DOWNLOADING` / 없음·서버 오류 → `IDLE` (다음 주기) |
| `DOWNLOADING` | ③ 다운로드하면서 SHA256 계산, 불일치 시 파일 삭제 | 성공 → `INSTALLING` / 실패 → `REPORTING` (`error_code` 기록) |
| `INSTALLING` | 다운로드 파일을 설치 위치로 옮기고 `current_version` 교체 | 성공·실패 모두 → `REPORTING` |
| `REPORTING` | ④ 결과 보고 (`error_code`가 비어 있으면 `SUCCESS`, 아니면 `FAILED`) | 보고 성공 → `pending_update`·`error_code` 비우고 `IDLE` / 보고 실패 → `REPORTING` (다음 주기) |

**`REPORTING`을 별도 상태로 두는 이유:** 설치 직후 보고가 실패하거나 프로세스가 죽어도 결과를 잃지 않기 위해서다.
보고가 성공할 때까지 이 상태에 머무르며 재시도한다.

### 재시작 시 동작

| 저장된 상태 | 재시작 후 |
|---|---|
| `IDLE` | 평소처럼 체크인 |
| `DOWNLOADING` | 부분 파일을 버리고 처음부터 다시 다운로드 (이어받기는 범위 밖) |
| `INSTALLING` | 설치를 다시 수행. 설치는 같은 입력에 대해 여러 번 실행해도 결과가 같아야 한다(멱등). |
| `REPORTING` | 보고를 다시 시도. 서버는 같은 `(vin, campaign_id)` 보고를 중복 수신할 수 있다. |

### 실패 코드

| 코드 | 발생 시점 |
|---|---|
| `DOWNLOAD_FAILED` | 네트워크 오류, 200이 아닌 응답 |
| `HASH_MISMATCH` | 받은 파일의 SHA256이 매니페스트와 다름 |
| `INSTALL_FAILED` | 설치 중 파일 오류 |

## 6. 로컬 파일 배치

```
${DATA_DIR}/
├── state.json                  # 영속 상태
├── state.json.tmp              # 원자적 저장용 임시 파일 (저장 중에만 존재)
├── downloads/<campaign_id>.bin # 다운로드 중·검증 대기 파일
└── firmware/current.bin        # 설치된 펌웨어
```

## 7. 부품 구성

```
cmd/agent/main.go ── 조립 ──▶ agent (상태 머신)
                               ├── state.Store        상태 로드/저장
                               ├── otaclient.Client   ①② 체크인, ④ 보고
                               ├── downloader         ③ 다운로드 + SHA256
                               └── installer          설치
```

`agent`는 각 부품을 인터페이스로 받는다. 테스트에서는 가짜 구현이나 `httptest` 서버를 끼워 ①~④ 한 바퀴를 검증한다.

## 8. 리소스 가이드

- 이미지: distroless/static 기반, 약 10MB 목표
- 메모리: 대당 수십 MB 이하. compose에서 `mem_limit`, `GOMEMLIMIT`로 상한 설정
- 테스트용 더미 펌웨어: 1~5MB 랜덤 바이트 (`head -c 5M /dev/urandom > fw.bin`)
- 컨테이너 로그는 `max-size`로 로테이션

## 9. 구현 계획

| # | 커밋 | 내용 |
|---|---|---|
| 0 | `chore: 프로젝트 초기 구성` | 뼈대 (완료) |
| 1 | `feat: 환경변수 기반 설정 로딩` | `internal/config` |
| 2 | `feat: 에이전트 상태 모델과 JSON 저장/복구` | `internal/state` |
| 3 | `feat: OTA 서버 체크인/결과 보고 클라이언트` | `internal/otaclient` |
| 4 | `feat: 펌웨어 다운로드와 SHA256 검증` | `internal/downloader` |
| 5 | `feat: 설치 시뮬레이션` | `internal/installer` |
| 6 | `feat: 상태 머신 루프와 main 조립` | `internal/agent`, `cmd/agent` |
| 7 | `chore: Dockerfile과 docker-compose 추가` | 이미지 빌드, N대 실행 |

### 이후 확장 후보

다운로드 속도 제한 → Range 이어받기 → 재시도 백오프 + jitter → 주행·배터리 설치 조건
→ A/B 슬롯과 롤백 → 매니페스트 서명 검증 → MQTT 푸시 → 차종·지역 타기팅
