package diag

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func probeAgainst(t *testing.T, target Target, handler http.HandlerFunc) Result {
	t.Helper()
	server := httptest.NewServer(handler)
	defer server.Close()
	target.URL = server.URL
	return (&Runner{}).Probe(context.Background(), target)
}

func TestGoodResponsePasses(t *testing.T) {
	result := probeAgainst(t,
		Target{ID: "x", Label: "X", Group: GroupDPI, MinimumBodyBytes: 8},
		func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, strings.Repeat("страница", 20))
		})
	if !result.OK() {
		t.Fatalf("исправный ответ отклонён: %+v", result)
	}
	if result.DelayMS < 1 {
		t.Fatalf("задержка не измерена: %+v", result)
	}
}

func TestStubPageIsRejected(t *testing.T) {
	// Interception typically answers 200 with a few bytes; counting the request as a
	// success would tell the user everything is fine while nothing loads.
	result := probeAgainst(t,
		Target{ID: "x", Label: "X", Group: GroupDPI, MinimumBodyBytes: 128},
		func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, "ok")
		})
	if result.OK() {
		t.Fatalf("заглушка принята за страницу: %+v", result)
	}
}

func TestBlockingStatusIsReportedAsBlock(t *testing.T) {
	result := probeAgainst(t,
		Target{ID: "x", Label: "X", Group: GroupDPI, MinimumBodyBytes: 0},
		func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusUnavailableForLegalReasons)
		})
	if result.Status != StatusFailed || !strings.Contains(result.Error, "блокировка") {
		t.Fatalf("451 разобран неверно: %+v", result)
	}
}

func TestServerErrorIsInconclusive(t *testing.T) {
	// A 5xx is the site's problem, not the tunnel's; calling it a bypass failure would
	// send the user chasing the wrong thing.
	result := probeAgainst(t,
		Target{ID: "x", Label: "X", Group: GroupDPI, MinimumBodyBytes: 0},
		func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusBadGateway)
		})
	if result.Status != StatusInconclusive {
		t.Fatalf("5xx классифицирован как %s", result.Status)
	}
}

func TestUnexpectedRedirectFails(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, strings.Repeat("страница", 20))
		}))
	defer elsewhere.Close()

	result := probeAgainst(t,
		Target{ID: "x", Label: "X", Group: GroupControl, MinimumBodyBytes: 8},
		func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, elsewhere.URL, http.StatusFound)
		})
	if result.OK() {
		t.Fatalf("перенаправление на чужой хост принято: %+v", result)
	}
}

func TestUnreachableTargetReportsReadableError(t *testing.T) {
	result := (&Runner{}).Probe(context.Background(), Target{
		ID: "x", Label: "X", Group: GroupControl,
		URL: "https://127.0.0.1:1/",
	})
	if result.OK() {
		t.Fatal("недоступная цель отмечена рабочей")
	}
	// Platform wording differs — Linux says "connection refused", Windows says "connectex:
	// ... actively refused it" — and neither belongs in front of a user.
	if result.Error != "соединение отклонено" {
		t.Fatalf("ошибка не переведена для пользователя: %q", result.Error)
	}
}

func TestTimeoutIsReportedAsNoAnswer(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			<-request.Context().Done()
		}))
	defer blocked.Close()

	runner := &Runner{Timeout: 300 * time.Millisecond}
	result := runner.Probe(context.Background(), Target{
		ID: "x", Label: "X", Group: GroupDPI, URL: blocked.URL,
	})
	if result.Error != "нет ответа за отведённое время" {
		t.Fatalf("таймаут описан как %q", result.Error)
	}
}

func passing(group Group, id string) Result {
	return Result{Target: Target{ID: id, Label: id, Group: group}, Status: StatusOK}
}

func failing(group Group, id string) Result {
	return Result{Target: Target{ID: id, Label: id, Group: group}, Status: StatusFailed}
}

func TestVerdictWithoutControl(t *testing.T) {
	verdict := Describe([]Result{
		failing(GroupControl, "google"),
		passing(GroupDPI, "kinozal"),
	})
	if verdict.ControlOK || verdict.BypassOK {
		t.Fatalf("вердикт: %+v", verdict)
	}
	if !strings.Contains(verdict.Headline, "недоступен") {
		t.Fatalf("заголовок = %q", verdict.Headline)
	}
}

func TestVerdictFullSuccess(t *testing.T) {
	verdict := Describe([]Result{
		passing(GroupControl, "google"),
		passing(GroupDPI, "kinozal"),
		passing(GroupDPI, "nnmclub"),
		passing(GroupIP, "telegram"),
	})
	if !verdict.BypassOK || verdict.Headline != "Всё работает" {
		t.Fatalf("вердикт: %+v", verdict)
	}
}

func TestVerdictSeparatesIPBlockFromDPI(t *testing.T) {
	verdict := Describe([]Result{
		passing(GroupControl, "google"),
		passing(GroupDPI, "kinozal"),
		passing(GroupDPI, "nnmclub"),
		failing(GroupIP, "telegram"),
	})
	// The bypass works; Telegram failing is about the exit address, and the advice has to
	// say so instead of blaming the bypass.
	if !verdict.BypassOK {
		t.Fatalf("вердикт: %+v", verdict)
	}
	if !strings.Contains(verdict.Detail, "смена сервера") {
		t.Fatalf("нет совета про смену сервера: %q", verdict.Detail)
	}
}

func TestInformationalTargetDoesNotCount(t *testing.T) {
	results := []Result{
		passing(GroupControl, "google"),
		{Target: Target{ID: "rutracker", Group: GroupDPI, Informational: true}, Status: StatusOK},
	}
	if BypassScore(results) != 0 {
		t.Fatalf("информационная цель попала в счёт: %d", BypassScore(results))
	}
	if BypassWorks(results) {
		t.Fatal("обход признан рабочим по информационной цели")
	}
}

func TestStandardTargetsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	controls := 0
	for _, target := range Targets {
		if seen[target.ID] {
			t.Fatalf("дублируется цель %s", target.ID)
		}
		seen[target.ID] = true
		if !strings.HasPrefix(target.URL, "https://") {
			t.Fatalf("цель %s не использует HTTPS: %s", target.ID, target.URL)
		}
		if target.Label == "" {
			t.Fatalf("цель %s без названия", target.ID)
		}
		if target.Group == GroupControl {
			controls++
		}
	}
	// More than one control: a single one going down would look like a broken tunnel.
	if controls < 2 {
		t.Fatalf("контрольных целей %d", controls)
	}
}
