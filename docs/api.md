# OTA API 계약 (에이전트 기준)

차량 에이전트가 기대하는 OTA 서버·CDN 인터페이스. 서버는 다른 언어로 구현해도 이 계약만 지키면 된다.

- 전송: HTTP, 본문은 JSON (`Content-Type: application/json`, UTF-8)
- 방향: 모든 요청은 에이전트가 먼저 보낸다
- 버전: 경로 접두사 `/api/v1`
- 타임아웃: 에이전트는 요청마다 타임아웃을 두고, 실패하면 다음 주기에 재시도한다

## ① 체크인 + ② 매니페스트

`POST /api/v1/vehicles/{vin}/checkin`

요청
```json
{
  "vin": "KMHEV6000000001",
  "current_version": "1.0.0",
  "state": "IDLE"
}
```

| 필드 | 타입 | 설명 |
|---|---|---|
| `vin` | string | 경로의 `{vin}`과 같아야 한다 |
| `current_version` | string | 현재 설치된 버전 |
| `state` | string | 에이전트 상태. 최소 버전에서는 `IDLE`에서만 체크인하므로 항상 `IDLE` |

응답 `200 OK` — 업데이트가 있을 때
```json
{
  "update_available": true,
  "update": {
    "campaign_id": "cmp-42",
    "target_version": "1.1.0",
    "url": "http://cdn/fw/1.1.0.bin",
    "sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
  }
}
```

응답 `200 OK` — 업데이트가 없을 때
```json
{ "update_available": false }
```

| 필드 | 타입 | 설명 |
|---|---|---|
| `update_available` | bool | 업데이트 여부 |
| `update.campaign_id` | string | 배포 캠페인 ID. ④ 보고에 그대로 사용 |
| `update.target_version` | string | 설치할 버전 |
| `update.url` | string | 펌웨어 다운로드 URL (CDN) |
| `update.sha256` | string | 펌웨어 SHA256, 소문자 hex 64자 |

200이 아닌 응답은 서버 오류로 보고 다음 주기에 다시 체크인한다.

## ③ 펌웨어 다운로드

`GET {update.url}`

- 응답 `200 OK`, 본문은 펌웨어 바이너리 (`application/octet-stream`)
- 200이 아니면 `DOWNLOAD_FAILED`로 처리한다

## ④ 결과 보고

`POST /api/v1/vehicles/{vin}/report`

요청
```json
{
  "vin": "KMHEV6000000001",
  "campaign_id": "cmp-42",
  "result": "SUCCESS",
  "current_version": "1.1.0",
  "error_code": ""
}
```

| 필드 | 타입 | 설명 |
|---|---|---|
| `campaign_id` | string | ②에서 받은 캠페인 ID |
| `result` | string | `SUCCESS` 또는 `FAILED` |
| `current_version` | string | 보고 시점에 설치된 버전 (실패하면 기존 버전) |
| `error_code` | string | 실패 코드. 성공이면 빈 문자열 |

응답: `2xx`면 성공으로 보고 본문은 무시한다. 그 외에는 다음 주기에 다시 보낸다.

**멱등성:** 에이전트는 보고 성공 후 상태를 저장하기 전에 죽으면 같은 보고를 다시 보낼 수 있다.
서버는 `(vin, campaign_id)` 기준으로 중복 보고를 안전하게 처리해야 한다.

## 실패 코드

| 코드 | 의미 |
|---|---|
| `DOWNLOAD_FAILED` | 다운로드 네트워크 오류 또는 200이 아닌 응답 |
| `HASH_MISMATCH` | SHA256 불일치 |
| `INSTALL_FAILED` | 설치 중 오류 |
