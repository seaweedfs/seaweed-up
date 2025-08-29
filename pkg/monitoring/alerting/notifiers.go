package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/fatih/color"
)

// ConsoleNotifier sends alerts to the console/terminal
type ConsoleNotifier struct {
	name string
}

// NewConsoleNotifier creates a new console notifier
func NewConsoleNotifier() *ConsoleNotifier {
	return &ConsoleNotifier{
		name: "console",
	}
}

// SendAlert sends an alert to the console
func (cn *ConsoleNotifier) SendAlert(ctx context.Context, alert Alert) error {
	timestamp := alert.StartsAt.Format("2006-01-02 15:04:05")
	
	switch alert.Status {
	case StatusFiring:
		switch alert.Severity {
		case SeverityCritical:
			color.Red("🚨 CRITICAL ALERT [%s] %s", timestamp, alert.Summary)
		case SeverityWarning:
			color.Yellow("⚠️  WARNING ALERT [%s] %s", timestamp, alert.Summary)
		case SeverityInfo:
			color.Cyan("ℹ️  INFO ALERT [%s] %s", timestamp, alert.Summary)
		}
		
		fmt.Printf("   Rule: %s\n", alert.RuleName)
		fmt.Printf("   Description: %s\n", alert.Description)
		fmt.Printf("   Value: %.2f (threshold: %.2f)\n", alert.Value, alert.Threshold)
		
		if len(alert.Labels) > 0 {
			fmt.Print("   Labels: ")
			var labelParts []string
			for k, v := range alert.Labels {
				labelParts = append(labelParts, fmt.Sprintf("%s=%s", k, v))
			}
			fmt.Println(strings.Join(labelParts, ", "))
		}
		
	case StatusResolved:
		color.Green("✅ RESOLVED [%s] %s", timestamp, alert.Summary)
		if alert.EndsAt != nil {
			duration := alert.EndsAt.Sub(alert.StartsAt)
			fmt.Printf("   Duration: %s\n", duration.String())
		}
	}
	
	fmt.Println()
	return nil
}

// GetName returns the notifier name
func (cn *ConsoleNotifier) GetName() string {
	return cn.name
}

// EmailNotifier sends alerts via email
type EmailNotifier struct {
	name       string
	smtpHost   string
	smtpPort   int
	username   string
	password   string
	from       string
	to         []string
	client     *http.Client
}

// EmailConfig contains email notification configuration
type EmailConfig struct {
	SMTPHost string   `json:"smtp_host"`
	SMTPPort int      `json:"smtp_port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
}

// NewEmailNotifier creates a new email notifier
func NewEmailNotifier(config EmailConfig) *EmailNotifier {
	return &EmailNotifier{
		name:     "email",
		smtpHost: config.SMTPHost,
		smtpPort: config.SMTPPort,
		username: config.Username,
		password: config.Password,
		from:     config.From,
		to:       config.To,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SendAlert sends an alert via email
func (en *EmailNotifier) SendAlert(ctx context.Context, alert Alert) error {
	if len(en.to) == 0 {
		return fmt.Errorf("no recipients configured")
	}
	
	subject := fmt.Sprintf("[%s] %s - %s", strings.ToUpper(string(alert.Severity)), alert.Status, alert.Summary)
	body := en.formatEmailBody(alert)
	
	// Create message
	message := fmt.Sprintf("From: %s\r\n", en.from)
	message += fmt.Sprintf("To: %s\r\n", strings.Join(en.to, ","))
	message += fmt.Sprintf("Subject: %s\r\n", subject)
	message += "Content-Type: text/html; charset=UTF-8\r\n"
	message += "\r\n"
	message += body
	
	// Send email
	addr := fmt.Sprintf("%s:%d", en.smtpHost, en.smtpPort)
	auth := smtp.PlainAuth("", en.username, en.password, en.smtpHost)
	
	return smtp.SendMail(addr, auth, en.from, en.to, []byte(message))
}

// formatEmailBody formats the alert as HTML email body
func (en *EmailNotifier) formatEmailBody(alert Alert) string {
	statusColor := "green"
	statusIcon := "✅"
	
	if alert.Status == StatusFiring {
		switch alert.Severity {
		case SeverityCritical:
			statusColor = "red"
			statusIcon = "🚨"
		case SeverityWarning:
			statusColor = "orange"
			statusIcon = "⚠️"
		case SeverityInfo:
			statusColor = "blue"
			statusIcon = "ℹ️"
		}
	}
	
	html := fmt.Sprintf(`
<html>
<head>
    <style>
        body { font-family: Arial, sans-serif; }
        .alert-header { background-color: %s; color: white; padding: 10px; border-radius: 5px; }
        .alert-body { padding: 15px; }
        .label { background-color: #f0f0f0; padding: 2px 5px; border-radius: 3px; margin: 2px; }
        table { border-collapse: collapse; width: 100%%; }
        td, th { border: 1px solid #ddd; padding: 8px; text-align: left; }
        th { background-color: #f2f2f2; }
    </style>
</head>
<body>
    <div class="alert-header">
        <h2>%s %s Alert: %s</h2>
    </div>
    <div class="alert-body">
        <table>
            <tr><th>Rule</th><td>%s</td></tr>
            <tr><th>Description</th><td>%s</td></tr>
            <tr><th>Status</th><td>%s</td></tr>
            <tr><th>Severity</th><td>%s</td></tr>
            <tr><th>Value</th><td>%.2f (threshold: %.2f)</td></tr>
            <tr><th>Started At</th><td>%s</td></tr>
            <tr><th>Updated At</th><td>%s</td></tr>
        </table>
`, statusColor, statusIcon, strings.ToUpper(string(alert.Severity)), alert.Summary,
		alert.RuleName, alert.Description, alert.Status, alert.Severity,
		alert.Value, alert.Threshold,
		alert.StartsAt.Format("2006-01-02 15:04:05"),
		alert.UpdatedAt.Format("2006-01-02 15:04:05"))
	
	if len(alert.Labels) > 0 {
		html += "\n        <h3>Labels:</h3>\n        <div>"
		for k, v := range alert.Labels {
			html += fmt.Sprintf(`<span class="label">%s=%s</span>`, k, v)
		}
		html += "</div>"
	}
	
	if len(alert.Annotations) > 0 {
		html += "\n        <h3>Annotations:</h3>\n        <table>"
		for k, v := range alert.Annotations {
			html += fmt.Sprintf("<tr><th>%s</th><td>%s</td></tr>", k, v)
		}
		html += "</table>"
	}
	
	html += `
    </div>
</body>
</html>`
	
	return html
}

// GetName returns the notifier name
func (en *EmailNotifier) GetName() string {
	return en.name
}

// WebhookNotifier sends alerts to a webhook URL
type WebhookNotifier struct {
	name       string
	url        string
	headers    map[string]string
	client     *http.Client
	template   string
}

// WebhookConfig contains webhook notification configuration
type WebhookConfig struct {
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Template string            `json:"template"`
}

// NewWebhookNotifier creates a new webhook notifier
func NewWebhookNotifier(config WebhookConfig) *WebhookNotifier {
	if config.Headers == nil {
		config.Headers = make(map[string]string)
	}
	
	// Set default content type if not specified
	if _, exists := config.Headers["Content-Type"]; !exists {
		config.Headers["Content-Type"] = "application/json"
	}
	
	return &WebhookNotifier{
		name:    "webhook",
		url:     config.URL,
		headers: config.Headers,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		template: config.Template,
	}
}

// SendAlert sends an alert to a webhook
func (wn *WebhookNotifier) SendAlert(ctx context.Context, alert Alert) error {
	var payload []byte
	var err error
	
	if wn.template != "" {
		// Use custom template
		payload = []byte(wn.interpolateTemplate(wn.template, alert))
	} else {
		// Use default JSON payload
		payload, err = json.Marshal(alert)
		if err != nil {
			return fmt.Errorf("failed to marshal alert: %w", err)
		}
	}
	
	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", wn.url, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	
	// Set headers
	for key, value := range wn.headers {
		req.Header.Set(key, value)
	}
	
	// Send request
	resp, err := wn.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	
	return nil
}

// interpolateTemplate replaces template variables in the webhook template
func (wn *WebhookNotifier) interpolateTemplate(template string, alert Alert) string {
	result := template
	
	// Replace alert fields
	result = strings.ReplaceAll(result, "{{.ID}}", alert.ID)
	result = strings.ReplaceAll(result, "{{.RuleName}}", alert.RuleName)
	result = strings.ReplaceAll(result, "{{.Summary}}", alert.Summary)
	result = strings.ReplaceAll(result, "{{.Description}}", alert.Description)
	result = strings.ReplaceAll(result, "{{.Severity}}", string(alert.Severity))
	result = strings.ReplaceAll(result, "{{.Status}}", string(alert.Status))
	result = strings.ReplaceAll(result, "{{.Value}}", fmt.Sprintf("%.2f", alert.Value))
	result = strings.ReplaceAll(result, "{{.Threshold}}", fmt.Sprintf("%.2f", alert.Threshold))
	result = strings.ReplaceAll(result, "{{.StartsAt}}", alert.StartsAt.Format(time.RFC3339))
	result = strings.ReplaceAll(result, "{{.UpdatedAt}}", alert.UpdatedAt.Format(time.RFC3339))
	
	// Replace metric fields if available
	if alert.MetricData != nil {
		result = strings.ReplaceAll(result, "{{.Component}}", alert.MetricData.Component)
		result = strings.ReplaceAll(result, "{{.Host}}", alert.MetricData.Host)
		result = strings.ReplaceAll(result, "{{.MetricName}}", alert.MetricData.MetricName)
	}
	
	return result
}

// GetName returns the notifier name
func (wn *WebhookNotifier) GetName() string {
	return wn.name
}

// SlackNotifier sends alerts to Slack
type SlackNotifier struct {
	name       string
	webhookURL string
	channel    string
	username   string
	client     *http.Client
}

// SlackPayload represents a Slack webhook payload
type SlackPayload struct {
	Channel     string            `json:"channel,omitempty"`
	Username    string            `json:"username,omitempty"`
	Text        string            `json:"text"`
	Attachments []SlackAttachment `json:"attachments,omitempty"`
}

// SlackAttachment represents a Slack message attachment
type SlackAttachment struct {
	Color     string       `json:"color"`
	Title     string       `json:"title"`
	Text      string       `json:"text"`
	Fields    []SlackField `json:"fields,omitempty"`
	Timestamp int64        `json:"ts"`
}

// SlackField represents a field in a Slack attachment
type SlackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

// NewSlackNotifier creates a new Slack notifier
func NewSlackNotifier(webhookURL, channel, username string) *SlackNotifier {
	return &SlackNotifier{
		name:       "slack",
		webhookURL: webhookURL,
		channel:    channel,
		username:   username,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SendAlert sends an alert to Slack
func (sn *SlackNotifier) SendAlert(ctx context.Context, alert Alert) error {
	color := "good"
	emoji := "✅"
	
	if alert.Status == StatusFiring {
		switch alert.Severity {
		case SeverityCritical:
			color = "danger"
			emoji = "🚨"
		case SeverityWarning:
			color = "warning"
			emoji = "⚠️"
		case SeverityInfo:
			color = "#36a64f"
			emoji = "ℹ️"
		}
	}
	
	text := fmt.Sprintf("%s *%s Alert*: %s", emoji, strings.ToUpper(string(alert.Severity)), alert.Summary)
	
	attachment := SlackAttachment{
		Color:     color,
		Title:     fmt.Sprintf("Alert: %s", alert.RuleName),
		Text:      alert.Description,
		Timestamp: alert.StartsAt.Unix(),
		Fields: []SlackField{
			{Title: "Status", Value: string(alert.Status), Short: true},
			{Title: "Severity", Value: string(alert.Severity), Short: true},
			{Title: "Value", Value: fmt.Sprintf("%.2f", alert.Value), Short: true},
			{Title: "Threshold", Value: fmt.Sprintf("%.2f", alert.Threshold), Short: true},
		},
	}
	
	if alert.MetricData != nil {
		attachment.Fields = append(attachment.Fields,
			SlackField{Title: "Component", Value: alert.MetricData.Component, Short: true},
			SlackField{Title: "Host", Value: alert.MetricData.Host, Short: true},
		)
	}
	
	payload := SlackPayload{
		Channel:     sn.channel,
		Username:    sn.username,
		Text:        text,
		Attachments: []SlackAttachment{attachment},
	}
	
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal Slack payload: %w", err)
	}
	
	req, err := http.NewRequestWithContext(ctx, "POST", sn.webhookURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := sn.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send Slack notification: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Slack webhook returned status %d", resp.StatusCode)
	}
	
	return nil
}

// GetName returns the notifier name
func (sn *SlackNotifier) GetName() string {
	return sn.name
}
