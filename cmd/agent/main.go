// cmd/agent는 차량 에이전트 실행 파일의 진입점이다.
//
// main 패키지는 "실행 가능한 프로그램"을 뜻하며, main 함수에서 실행이 시작된다.
// 여기서는 설정을 읽고 부품을 조립해서 실행하는 일만 하고,
// 실제 로직은 internal/ 아래 패키지에 둔다(main을 얇게 유지하는 Go 관례).
package main

import (
	// 표준 라이브러리 import. 경로 마지막 이름(slog, os)으로 코드에서 사용한다.
	"log/slog"
	"os"

	// 이 모듈 안의 패키지는 go.mod의 모듈 경로를 앞에 붙여 import한다.
	// internal/ 아래 패키지는 이 모듈 안에서만 import할 수 있다(컴파일러가 강제).
	"github.com/xoruddl/car_agent_demo/internal/config"
)

func main() {
	// 로그를 JSON 한 줄씩 표준 출력으로 내보낸다.
	// 컨테이너 환경에서는 표준 출력이 `docker logs`로 모이고,
	// JSON 형식이면 나중에 로그 수집 도구가 필드별로 검색하기 쉽다.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 함수를 호출하지 않고 이름만 넘긴다(os.Getenv 뒤에 괄호가 없음).
	// Load 안에서 필요할 때 os.Getenv("VIN")처럼 호출된다.
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		// 설정이 잘못되면 더 진행해도 의미가 없으므로 종료한다.
		// 종료 코드 1(실패)을 주면 docker 재시작 정책이나 스크립트가 실패를 알아챌 수 있다.
		logger.Error("invalid config", "error", err)
		os.Exit(1)
	}

	// slog는 메시지 뒤에 "키", 값 쌍을 이어서 적는다. JSON의 필드가 된다.
	logger.Info("vehicle agent started",
		"vin", cfg.VIN,
		"server_url", cfg.ServerURL,
		"checkin_interval", cfg.CheckinInterval.String(), // "30s"처럼 읽기 쉬운 문자열로
		"data_dir", cfg.DataDir,
		"initial_version", cfg.InitialVersion,
	)
}
