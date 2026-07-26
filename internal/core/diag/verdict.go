package diag

import "strings"

// Verdict is the plain-language summary of a diagnostics run.
type Verdict struct {
	// Headline is the one line that answers "does it work".
	Headline string `json:"headline"`
	// Detail explains what the results imply and what to try next.
	Detail string `json:"detail"`
	// ControlOK reports whether the internet is reachable at all through the tunnel.
	ControlOK bool `json:"controlOk"`
	// BypassOK reports whether blocked sites load.
	BypassOK bool `json:"bypassOk"`
}

// ControlWorks reports whether plain reachability succeeded. Without it every other
// result is noise: nothing can load when nothing is reachable.
func ControlWorks(results []Result) bool {
	for _, result := range results {
		if result.Target.Group == GroupControl && result.OK() {
			return true
		}
	}
	return false
}

// BypassScore counts how many DPI-blocked sites loaded, ignoring the informational ones.
func BypassScore(results []Result) int {
	score := 0
	for _, result := range results {
		if result.Target.Group == GroupDPI && !result.Target.Informational && result.OK() {
			score++
		}
	}
	return score
}

// BypassWorks applies the same rule as the Android client: reachability plus at least two
// blocked sites loading.
func BypassWorks(results []Result) bool {
	return ControlWorks(results) && BypassScore(results) >= RequiredDPISuccesses
}

// Describe turns results into a verdict a user can act on.
func Describe(results []Result) Verdict {
	if len(results) == 0 {
		return Verdict{Headline: "Проверка не выполнялась"}
	}

	control := ControlWorks(results)
	score := BypassScore(results)
	verdict := Verdict{ControlOK: control, BypassOK: control && score >= RequiredDPISuccesses}

	if !control {
		verdict.Headline = "Интернет через туннель недоступен"
		verdict.Detail = "Не отвечают даже обычные сайты. Проверьте подключение, " +
			"попробуйте другой сервер или переподключитесь."
		return verdict
	}

	switch {
	case score >= RequiredDPISuccesses:
		verdict.Headline = "Всё работает"
		verdict.Detail = "Обычные сайты и заблокированные ресурсы открываются через туннель."
	case score == 1:
		verdict.Headline = "Обход работает частично"
		verdict.Detail = "Часть заблокированных сайтов не открывается. Смените сервер " +
			"или повторите проверку: провайдер мог ограничить конкретный маршрут."
	default:
		verdict.Headline = "Интернет есть, обход не подтверждён"
		verdict.Detail = "Обычные сайты открываются, заблокированные — нет. " +
			"Похоже, трафик идёт мимо туннеля или сервер не справляется с обходом."
	}

	if notes := extras(results); notes != "" {
		verdict.Detail += " " + notes
	}
	return verdict
}

// extras adds the observations that do not change the verdict but change what the user
// should do next.
func extras(results []Result) string {
	notes := []string{}
	for _, result := range results {
		switch result.Target.Group {
		case GroupIP:
			if !result.OK() {
				notes = append(notes,
					"Telegram недоступен даже через туннель — вероятна блокировка по адресу "+
						"сервера, помогает смена сервера.")
			}
		case GroupMedia:
			if !result.OK() {
				notes = append(notes,
					"YouTube не открылся: возможна отдельная деградация видео у провайдера.")
			}
		case GroupDPI:
			if result.Status == StatusInconclusive {
				notes = append(notes,
					result.Target.Label+" ответил ошибкой сервера — результат не показателен.")
			}
		}
	}
	return strings.Join(notes, " ")
}
