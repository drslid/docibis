package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"docibis/internal/render"
	"docibis/internal/store"
	"docibis/internal/version"
)

// An alert rule watches one value of the service, measured every minute: it
// fires when the value stays above its threshold for its duration, and
// tells the recipients when it fires and when it is resolved.
type alertRule struct {
	id, unit, label string
	severity        AlertRuleSeverity
	min, max        float32
	defaults        AlertRuleConfig
}

var alertRules = []alertRule{
	{"engine_down", "state", "Moteur de rendu LibreOffice indisponible", AlertRuleSeverityCritical, 0, 0, AlertRuleConfig{Enabled: true, Threshold: 0, ForMinutes: 2}},
	{"db_down", "state", "Base de données injoignable", AlertRuleSeverityCritical, 0, 0, AlertRuleConfig{Enabled: true, Threshold: 0, ForMinutes: 1}},
	{"lease_lost", "state", "Verrou de la base pris par une autre tâche", AlertRuleSeverityCritical, 0, 0, AlertRuleConfig{Enabled: true, Threshold: 0, ForMinutes: 0}},
	{"error_rate", "pct", "Documents en échec (15 dernières minutes)", AlertRuleSeverityCritical, 1, 100, AlertRuleConfig{Enabled: true, Threshold: 10, ForMinutes: 5}},
	{"latency_p95", "ms", "Durée des rendus, 95e centile (15 dernières minutes)", AlertRuleSeverityWarning, 1000, 60000, AlertRuleConfig{Enabled: true, Threshold: 20000, ForMinutes: 10}},
	{"queue", "count", "Demandes en attente d'un worker", AlertRuleSeverityWarning, 1, 1000, AlertRuleConfig{Enabled: true, Threshold: 10, ForMinutes: 2}},
	{"http_5xx", "per_min", "Erreurs serveur (5xx) par minute", AlertRuleSeverityCritical, 1, 10000, AlertRuleConfig{Enabled: true, Threshold: 5, ForMinutes: 5}},
	{"reference_differs", "count", "Modèles différents de leur PDF de référence", AlertRuleSeverityWarning, 0, 1000, AlertRuleConfig{Enabled: false, Threshold: 0, ForMinutes: 0}},
	{"key_expiring", "count", "Clés API qui expirent dans les 14 jours", AlertRuleSeverityWarning, 0, 1000, AlertRuleConfig{Enabled: true, Threshold: 0, ForMinutes: 0}},
	{"cpu_high", "pct", "Processeur de la tâche utilisé (moyenne de la minute)", AlertRuleSeverityWarning, 10, 100, AlertRuleConfig{Enabled: true, Threshold: 85, ForMinutes: 10}},
	{"memory_high", "pct", "Mémoire de la tâche utilisée", AlertRuleSeverityCritical, 10, 100, AlertRuleConfig{Enabled: true, Threshold: 90, ForMinutes: 5}},
}

const alertsConfigKey = "alerts.config"

// alertEngine keeps the state of each rule between two measures.
type alertEngine struct {
	mu     sync.Mutex
	states map[string]*ruleState
}

type ruleState struct {
	state     AlertStateState
	value     float32
	threshold float32
	since     time.Time // start of the condition (pending) or of the alert
}

func defaultAlertConfig() AlertConfig {
	c := AlertConfig{Enabled: false, Recipients: []string{}, Rules: map[string]AlertRuleConfig{}}
	for _, r := range alertRules {
		c.Rules[r.id] = r.defaults
	}
	return c
}

// alertConfig reads the saved configuration, completed with the rules added
// since.
func (s *Server) alertConfig(ctx context.Context) (AlertConfig, error) {
	c := defaultAlertConfig()
	raw, ok, err := s.db.Load().Setting(ctx, alertsConfigKey)
	if err != nil || !ok {
		return c, err
	}
	var saved AlertConfig
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		s.logger.Warn("alert configuration unreadable: defaults used", "error", err)
		return c, nil
	}
	c.Enabled, c.TeamsWebhook = saved.Enabled, saved.TeamsWebhook
	if saved.Recipients != nil {
		c.Recipients = saved.Recipients
	}
	for id, rc := range saved.Rules {
		if _, known := c.Rules[id]; known {
			c.Rules[id] = rc
		}
	}
	return c, nil
}

// maskWebhook shows the start of the Teams address only: the rest is its key.
func maskWebhook(u *string) *string {
	if u == nil || *u == "" {
		return nil
	}
	shown := *u
	if parsed, err := url.Parse(shown); err == nil {
		shown = parsed.Scheme + "://" + parsed.Host
	}
	masked := shown + "/••••••"
	return &masked
}

func (s *Server) smtpConfigured() bool {
	cfg := s.cfg.Load()
	return cfg.String("smtp_host") != "" && cfg.String("smtp_from") != ""
}

func (s *Server) alertsInfo(ctx context.Context) (AlertsInfo, error) {
	c, err := s.alertConfig(ctx)
	if err != nil {
		return AlertsInfo{}, err
	}
	teams := c.TeamsWebhook != nil && *c.TeamsWebhook != ""
	c.TeamsWebhook = maskWebhook(c.TeamsWebhook)
	out := AlertsInfo{Config: c, EmailConfigured: s.smtpConfigured(), TeamsConfigured: teams, Rules: []AlertRule{}, States: s.alertStates(c), Events: []AlertEvent{}}
	for _, r := range alertRules {
		out.Rules = append(out.Rules, AlertRule{Id: r.id, Unit: r.unit, Severity: r.severity, Min: r.min, Max: r.max, Defaults: r.defaults})
	}
	events, err := s.db.Load().AlertEvents(ctx, 100)
	if err != nil {
		return out, err
	}
	for _, e := range events {
		out.Events = append(out.Events, AlertEvent{Id: e.ID, At: e.CreatedAt, Rule: e.Rule, State: AlertEventState(e.State), Severity: AlertEventSeverity(e.Severity),
			Unit: e.Unit, Value: float32(e.Value), Threshold: float32(e.Threshold), Notified: e.Notified})
	}
	return out, nil
}

func (s *Server) alertStates(c AlertConfig) []AlertState {
	s.alerts.mu.Lock()
	defer s.alerts.mu.Unlock()
	out := []AlertState{}
	for _, r := range alertRules {
		rc := c.Rules[r.id]
		st := AlertState{Rule: r.id, State: AlertStateStateOk, Severity: AlertStateSeverity(r.severity), Unit: r.unit, Threshold: rc.Threshold}
		if cur := s.alerts.states[r.id]; cur != nil {
			st.State, st.Value = cur.state, cur.value
			if !cur.since.IsZero() {
				since := cur.since
				st.Since = &since
			}
		}
		if !c.Enabled || !rc.Enabled {
			st.State = AlertStateStateDisabled
		}
		out = append(out, st)
	}
	return out
}

func (s *Server) GetAlerts(ctx context.Context, _ GetAlertsRequestObject) (GetAlertsResponseObject, error) {
	out, err := s.alertsInfo(ctx)
	if err != nil {
		return nil, err
	}
	return GetAlerts200JSONResponse(out), nil
}

func (s *Server) SaveAlerts(ctx context.Context, req SaveAlertsRequestObject) (SaveAlertsResponseObject, error) {
	if req.Body == nil {
		return SaveAlerts400ApplicationProblemPlusJSONResponse{badRequest("Configuration absente.")}, nil
	}
	in := *req.Body
	old, err := s.alertConfig(ctx)
	if err != nil {
		return nil, err
	}
	c := defaultAlertConfig()
	c.Enabled = in.Enabled
	var problems []string
	seen := map[string]bool{}
	for _, r := range in.Recipients {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		addr, err := mail.ParseAddress(r)
		if err != nil || addr.Name != "" {
			problems = append(problems, fmt.Sprintf("adresse %q invalide", r))
			continue
		}
		if a := strings.ToLower(addr.Address); !seen[a] {
			seen[a] = true
			c.Recipients = append(c.Recipients, a)
		}
	}
	if in.TeamsWebhook != nil {
		hook := strings.TrimSpace(*in.TeamsWebhook)
		switch {
		case strings.Contains(hook, "••••••"):
			c.TeamsWebhook = old.TeamsWebhook
		case hook == "":
		case !strings.HasPrefix(hook, "https://"):
			problems = append(problems, "l'adresse Teams doit commencer par https://")
		default:
			c.TeamsWebhook = &hook
		}
	}
	for _, r := range alertRules {
		rc, ok := in.Rules[r.id]
		if !ok {
			continue
		}
		if r.max > r.min && (rc.Threshold < r.min || rc.Threshold > r.max) {
			problems = append(problems, fmt.Sprintf("%s : seuil entre %g et %g", r.label, r.min, r.max))
			continue
		}
		if r.max == r.min {
			rc.Threshold = r.min
		}
		if rc.ForMinutes < 0 || rc.ForMinutes > 1440 {
			problems = append(problems, fmt.Sprintf("%s : durée entre 0 et 1440 minutes", r.label))
			continue
		}
		c.Rules[r.id] = rc
	}
	if len(problems) > 0 {
		return SaveAlerts400ApplicationProblemPlusJSONResponse{badRequest("Configuration refusée : " + strings.Join(problems, " ; ") + ".")}, nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	if err := s.db.Load().SetSetting(ctx, alertsConfigKey, string(raw)); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "alerts.update", "", fmt.Sprintf("actives : %t, %d destinataires, Teams : %t", c.Enabled, len(c.Recipients), c.TeamsWebhook != nil))
	out, err := s.alertsInfo(ctx)
	if err != nil {
		return nil, err
	}
	return SaveAlerts200JSONResponse(out), nil
}

func (s *Server) TestAlert(ctx context.Context, _ TestAlertRequestObject) (TestAlertResponseObject, error) {
	c, err := s.alertConfig(ctx)
	if err != nil {
		return nil, err
	}
	instance := s.cfg.Load().String("instance_name")
	subject := fmt.Sprintf("[DocIbis %s] Alerte d'essai", instance)
	text := fmt.Sprintf("Ceci est une alerte d'essai envoyée depuis la page Monitoring de DocIbis %s (version %s). Les alertes arriveront ici.", instance, version.Version)
	sent, errs := s.notify(ctx, c, subject, text, "good")
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "alerts.test", "", strings.Join(sent, ", "))
	out := AlertTest{Sent: sent}
	if len(errs) > 0 {
		out.Errors = &errs
	}
	if out.Sent == nil {
		out.Sent = []string{}
	}
	return TestAlert200JSONResponse(out), nil
}

// notify sends a message to the recipients by e-mail and to Teams; it
// returns the channels that accepted it and the errors of the others.
func (s *Server) notify(ctx context.Context, c AlertConfig, subject, text, color string) (sent, errs []string) {
	if s.smtpConfigured() && len(c.Recipients) > 0 {
		if err := s.sendMail(c.Recipients, subject, text); err != nil {
			s.logger.Warn("alert e-mail not sent", "error", err)
			errs = append(errs, "e-mail : "+err.Error())
		} else {
			sent = append(sent, "email")
		}
	}
	if c.TeamsWebhook != nil && *c.TeamsWebhook != "" {
		if err := postTeams(ctx, *c.TeamsWebhook, subject, text, color); err != nil {
			s.logger.Warn("alert not posted to Teams", "error", err)
			errs = append(errs, "Teams : "+err.Error())
		} else {
			sent = append(sent, "teams")
		}
	}
	if len(sent) == 0 && len(errs) == 0 {
		errs = append(errs, "aucun destinataire : réglez un serveur SMTP et des adresses, ou un flux Teams")
	}
	return sent, errs
}

// sendMail sends a plain text e-mail: STARTTLS on 587, TLS on 465.
func (s *Server) sendMail(to []string, subject, text string) error {
	cfg := s.cfg.Load()
	host, port := cfg.String("smtp_host"), cfg.Int("smtp_port")
	from := cfg.String("smtp_from")
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\nTo: %s\r\nSubject: =?UTF-8?B?%s?=\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n",
		from, strings.Join(to, ", "), b64(subject), time.Now().Format(time.RFC1123Z), strings.ReplaceAll(text, "\n", "\r\n"))
	var auth smtp.Auth
	if user := cfg.String("smtp_username"); user != "" {
		auth = smtp.PlainAuth("", user, os.Getenv("SMTP_PASSWORD"), host)
	}
	if port != 465 {
		return smtp.SendMail(addr, auth, from, to, msg.Bytes())
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, t := range to {
		if err := client.Rcpt(t); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg.Bytes()); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// postTeams posts an Adaptive Card to a Teams Workflows webhook.
func postTeams(ctx context.Context, hook, title, text, color string) error {
	card := map[string]any{
		"type": "message",
		"attachments": []any{map[string]any{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"type":    "AdaptiveCard",
				"version": "1.4",
				"body": []any{
					map[string]any{"type": "TextBlock", "text": title, "weight": "Bolder", "size": "Medium", "color": color, "wrap": true},
					map[string]any{"type": "TextBlock", "text": text, "wrap": true},
				},
			},
		}},
	}
	raw, err := json.Marshal(card)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("flux injoignable")
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("le flux a répondu %d", res.StatusCode)
	}
	return nil
}

// WatchAlerts measures the rules every minute until ctx ends.
func (s *Server) WatchAlerts(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.evaluateAlerts(ctx)
		}
	}
}

// measure reads the value of each rule now.
func (s *Server) measure(ctx context.Context) map[string]float32 {
	st := s.db.Load()
	now := time.Now().UTC()
	v := map[string]float32{}
	box := s.renderer.Load()
	if box == nil {
		v["engine_down"] = 1
	} else if p, ok := box.Renderer.(interface{ Stats() render.Stats }); ok {
		v["queue"] = float32(p.Stats().Queued)
	}
	ping, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if st.Ping(ping) != nil {
		v["db_down"] = 1
		return v
	}
	if st.Replication().LeaseLost {
		v["lease_lost"] = 1
	}
	if cpu, mem, ok := s.system.recent(1); ok {
		v["cpu_high"] = round1(cpu)
		if mem > 0 {
			v["memory_high"] = round1(mem)
		}
	}
	series := s.traffic.series(now)
	v["http_5xx"] = float32(series[len(series)-2].Errors)
	if times, err := st.RenderTimes(ctx, now.Add(-15*time.Minute)); err == nil {
		var durations []int
		failed, counted := 0, 0
		for _, t := range times {
			switch t.Status {
			case "error":
				failed++
				counted++
			case "ok":
				counted++
				durations = append(durations, t.DurationMs)
			}
		}
		if counted >= 3 {
			v["error_rate"] = float32(math.Round(float64(failed)*1000/float64(counted)) / 10)
		}
		sort.Ints(durations)
		v["latency_p95"] = float32(percentile(durations, 0.95))
	}
	if keys, err := st.APIKeys(ctx); err == nil {
		soon := now.AddDate(0, 0, 14)
		for _, k := range keys {
			if k.RevokedAt == nil && k.ExpiresAt != nil && k.ExpiresAt.After(now) && k.ExpiresAt.Before(soon) {
				v["key_expiring"]++
			}
		}
	}
	if templates, err := st.Templates(ctx, ""); err == nil {
		for _, t := range templates {
			var res CheckResult
			if t.CheckResult != "" && json.Unmarshal([]byte(t.CheckResult), &res) == nil && !res.Ok {
				v["reference_differs"]++
			}
		}
	}
	return v
}

// evaluateAlerts moves each rule between ok, pending and firing, and tells
// the recipients when an alert fires or ends.
func (s *Server) evaluateAlerts(ctx context.Context) {
	if s.db.Load() == nil || s.cfg.Load() == nil {
		return
	}
	c, err := s.alertConfig(ctx)
	if err != nil {
		s.logger.Warn("alert configuration not read", "error", err)
		return
	}
	values := s.measure(ctx)
	now := time.Now().UTC()
	type change struct {
		rule  alertRule
		state string
		value float32
		rc    AlertRuleConfig
	}
	var changes []change
	s.alerts.mu.Lock()
	if s.alerts.states == nil {
		s.alerts.states = map[string]*ruleState{}
	}
	for _, r := range alertRules {
		rc := c.Rules[r.id]
		cur := s.alerts.states[r.id]
		if cur == nil {
			cur = &ruleState{state: AlertStateStateOk}
			s.alerts.states[r.id] = cur
		}
		cur.value, cur.threshold = values[r.id], rc.Threshold
		if !c.Enabled || !rc.Enabled {
			cur.state, cur.since = AlertStateStateOk, time.Time{}
			continue
		}
		breached := values[r.id] > rc.Threshold
		switch {
		case breached && cur.state == AlertStateStateOk:
			cur.state, cur.since = AlertStateStatePending, now
			if rc.ForMinutes == 0 {
				cur.state = AlertStateStateFiring
				changes = append(changes, change{r, "firing", cur.value, rc})
			}
		case breached && cur.state == AlertStateStatePending && now.Sub(cur.since) >= time.Duration(rc.ForMinutes)*time.Minute:
			cur.state, cur.since = AlertStateStateFiring, now
			changes = append(changes, change{r, "firing", cur.value, rc})
		case !breached && cur.state == AlertStateStateFiring:
			cur.state, cur.since = AlertStateStateOk, time.Time{}
			changes = append(changes, change{r, "resolved", cur.value, rc})
		case !breached:
			cur.state, cur.since = AlertStateStateOk, time.Time{}
		}
	}
	s.alerts.mu.Unlock()

	instance := s.cfg.Load().String("instance_name")
	for _, ch := range changes {
		subject := fmt.Sprintf("[DocIbis %s] %s", instance, ch.rule.label)
		text, color := fmt.Sprintf("Alerte : %s.\nValeur : %s ; seuil : %s.", ch.rule.label, ruleValue(ch.value, ch.rule.unit), ruleValue(ch.rc.Threshold, ch.rule.unit)), "attention"
		if ch.state == "resolved" {
			subject += " : résolu"
			text, color = fmt.Sprintf("Fin de l'alerte : %s.\nValeur actuelle : %s.", ch.rule.label, ruleValue(ch.value, ch.rule.unit)), "good"
		}
		if base := s.cfg.Load().String("public_url"); base != "" {
			text += "\nMonitoring : " + strings.TrimRight(base, "/") + "/monitoring"
		}
		sent, _ := s.notify(ctx, c, subject, text, color)
		level := s.logger.Warn
		if ch.state == "resolved" {
			level = s.logger.Info
		}
		level("alert "+ch.state, "rule", ch.rule.id, "value", ch.value, "threshold", ch.rc.Threshold, "notified", strings.Join(sent, ","))
		e := &store.AlertEvent{Rule: ch.rule.id, State: ch.state, Severity: string(ch.rule.severity), Unit: ch.rule.unit,
			Value: float64(ch.value), Threshold: float64(ch.rc.Threshold), Notified: strings.Join(sent, ", ")}
		if err := s.db.Load().RecordAlertEvent(context.WithoutCancel(ctx), e); err != nil {
			s.logger.Warn("alert event not recorded", "error", err)
		}
	}
}

func ruleValue(v float32, unit string) string {
	n := strconv.FormatFloat(float64(v), 'f', -1, 32)
	switch unit {
	case "pct":
		return n + " %"
	case "ms":
		return n + " ms"
	case "per_min":
		return n + " par minute"
	case "state":
		if v > 0 {
			return "en panne"
		}
		return "en marche"
	}
	return n
}

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
