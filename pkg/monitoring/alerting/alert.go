package alerting

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/seaweedfs/seaweed-up/pkg/monitoring/metrics"
)

// AlertSeverity represents the severity level of an alert
type AlertSeverity string

const (
	SeverityCritical AlertSeverity = "critical"
	SeverityWarning  AlertSeverity = "warning"
	SeverityInfo     AlertSeverity = "info"
)

// AlertStatus represents the current status of an alert
type AlertStatus string

const (
	StatusFiring   AlertStatus = "firing"
	StatusResolved AlertStatus = "resolved"
	StatusSilenced AlertStatus = "silenced"
)

// Alert represents a single alert
type Alert struct {
	ID          string                 `json:"id"`
	RuleName    string                 `json:"rule_name"`
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	Severity    AlertSeverity          `json:"severity"`
	Status      AlertStatus            `json:"status"`
	Labels      map[string]string      `json:"labels"`
	Annotations map[string]string      `json:"annotations"`
	StartsAt    time.Time              `json:"starts_at"`
	EndsAt      *time.Time             `json:"ends_at,omitempty"`
	UpdatedAt   time.Time              `json:"updated_at"`
	Value       float64                `json:"value"`
	Threshold   float64                `json:"threshold"`
	MetricData  *metrics.MetricPoint   `json:"metric_data,omitempty"`
}

// AlertRule defines conditions for triggering alerts
type AlertRule struct {
	Name        string                 `json:"name"`
	Query       metrics.MetricsQuery   `json:"query"`
	Condition   AlertCondition         `json:"condition"`
	Threshold   float64                `json:"threshold"`
	Duration    time.Duration          `json:"duration"`
	Severity    AlertSeverity          `json:"severity"`
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	Labels      map[string]string      `json:"labels"`
	Annotations map[string]string      `json:"annotations"`
	Enabled     bool                   `json:"enabled"`
}

// AlertCondition represents different alert conditions
type AlertCondition string

const (
	ConditionGreaterThan    AlertCondition = "gt"
	ConditionLessThan      AlertCondition = "lt"
	ConditionEqual         AlertCondition = "eq"
	ConditionNotEqual      AlertCondition = "ne"
	ConditionAbsent        AlertCondition = "absent"
)

// AlertManager manages alerts and rules
type AlertManager struct {
	rules         []AlertRule
	activeAlerts  map[string]*Alert
	metricsStore  metrics.MetricsStorage
	notifiers     []AlertNotifier
	checkInterval time.Duration
	running       bool
	stopChan      chan struct{}
}

// AlertNotifier interface for sending alert notifications
type AlertNotifier interface {
	SendAlert(ctx context.Context, alert Alert) error
	GetName() string
}

// NewAlertManager creates a new alert manager
func NewAlertManager(metricsStore metrics.MetricsStorage, checkInterval time.Duration) *AlertManager {
	return &AlertManager{
		rules:         make([]AlertRule, 0),
		activeAlerts:  make(map[string]*Alert),
		metricsStore:  metricsStore,
		notifiers:     make([]AlertNotifier, 0),
		checkInterval: checkInterval,
		stopChan:      make(chan struct{}),
	}
}

// AddRule adds an alert rule
func (am *AlertManager) AddRule(rule AlertRule) {
	am.rules = append(am.rules, rule)
}

// AddNotifier adds an alert notifier
func (am *AlertManager) AddNotifier(notifier AlertNotifier) {
	am.notifiers = append(am.notifiers, notifier)
}

// Start begins alert monitoring
func (am *AlertManager) Start(ctx context.Context) error {
	if am.running {
		return fmt.Errorf("alert manager is already running")
	}
	
	am.running = true
	ticker := time.NewTicker(am.checkInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			if err := am.evaluateRules(ctx); err != nil {
				fmt.Printf("Error evaluating alert rules: %v\n", err)
			}
		case <-am.stopChan:
			am.running = false
			return nil
		case <-ctx.Done():
			am.running = false
			return ctx.Err()
		}
	}
}

// Stop stops alert monitoring
func (am *AlertManager) Stop() {
	if am.running {
		close(am.stopChan)
	}
}

// evaluateRules evaluates all alert rules
func (am *AlertManager) evaluateRules(ctx context.Context) error {
	for _, rule := range am.rules {
		if !rule.Enabled {
			continue
		}
		
		if err := am.evaluateRule(ctx, rule); err != nil {
			fmt.Printf("Error evaluating rule %s: %v\n", rule.Name, err)
			continue
		}
	}
	
	return nil
}

// evaluateRule evaluates a single alert rule
func (am *AlertManager) evaluateRule(ctx context.Context, rule AlertRule) error {
	// Query metrics for this rule
	metricPoints, err := am.metricsStore.Query(ctx, rule.Query)
	if err != nil {
		return fmt.Errorf("failed to query metrics: %w", err)
	}
	
	// Check if condition is met
	for _, metricPoint := range metricPoints {
		alertID := am.generateAlertID(rule, metricPoint)
		
		conditionMet := am.evaluateCondition(rule.Condition, metricPoint.Value, rule.Threshold)
		existingAlert, alertExists := am.activeAlerts[alertID]
		
		if conditionMet {
			if !alertExists {
				// Create new alert
				alert := &Alert{
					ID:          alertID,
					RuleName:    rule.Name,
					Summary:     am.interpolateTemplate(rule.Summary, metricPoint),
					Description: am.interpolateTemplate(rule.Description, metricPoint),
					Severity:    rule.Severity,
					Status:      StatusFiring,
					Labels:      am.mergeLabels(rule.Labels, metricPoint),
					Annotations: am.interpolateAnnotations(rule.Annotations, metricPoint),
					StartsAt:    time.Now(),
					UpdatedAt:   time.Now(),
					Value:       metricPoint.Value,
					Threshold:   rule.Threshold,
					MetricData:  &metricPoint,
				}
				
				am.activeAlerts[alertID] = alert
				
				// Send notifications
				for _, notifier := range am.notifiers {
					if err := notifier.SendAlert(ctx, *alert); err != nil {
						fmt.Printf("Failed to send alert via %s: %v\n", notifier.GetName(), err)
					}
				}
			} else {
				// Update existing alert
				existingAlert.UpdatedAt = time.Now()
				existingAlert.Value = metricPoint.Value
				existingAlert.MetricData = &metricPoint
			}
		} else {
			if alertExists && existingAlert.Status == StatusFiring {
				// Resolve alert
				now := time.Now()
				existingAlert.Status = StatusResolved
				existingAlert.EndsAt = &now
				existingAlert.UpdatedAt = now
				
				// Send resolution notification
				for _, notifier := range am.notifiers {
					if err := notifier.SendAlert(ctx, *existingAlert); err != nil {
						fmt.Printf("Failed to send alert resolution via %s: %v\n", notifier.GetName(), err)
					}
				}
				
				// Remove from active alerts after a delay
				go func(id string) {
					time.Sleep(5 * time.Minute)
					delete(am.activeAlerts, id)
				}(alertID)
			}
		}
	}
	
	return nil
}

// evaluateCondition evaluates if a condition is met
func (am *AlertManager) evaluateCondition(condition AlertCondition, value, threshold float64) bool {
	switch condition {
	case ConditionGreaterThan:
		return value > threshold
	case ConditionLessThan:
		return value < threshold
	case ConditionEqual:
		return value == threshold
	case ConditionNotEqual:
		return value != threshold
	case ConditionAbsent:
		// This would require different logic - checking if no metrics exist
		return false
	default:
		return false
	}
}

// generateAlertID generates a unique ID for an alert
func (am *AlertManager) generateAlertID(rule AlertRule, metric metrics.MetricPoint) string {
	return fmt.Sprintf("%s-%s-%s-%s", rule.Name, metric.Component, metric.Host, metric.MetricName)
}

// mergeLabels merges rule labels with metric labels
func (am *AlertManager) mergeLabels(ruleLabels map[string]string, metric metrics.MetricPoint) map[string]string {
	labels := make(map[string]string)
	
	// Add metric tags as labels
	for k, v := range metric.Tags {
		labels[k] = v
	}
	
	// Add rule labels (override metric tags if same key)
	for k, v := range ruleLabels {
		labels[k] = v
	}
	
	// Add standard labels
	labels["component"] = metric.Component
	labels["host"] = metric.Host
	labels["metric"] = metric.MetricName
	
	return labels
}

// interpolateTemplate replaces template variables in strings
func (am *AlertManager) interpolateTemplate(template string, metric metrics.MetricPoint) string {
	result := template
	result = strings.ReplaceAll(result, "{{.Component}}", metric.Component)
	result = strings.ReplaceAll(result, "{{.Host}}", metric.Host)
	result = strings.ReplaceAll(result, "{{.MetricName}}", metric.MetricName)
	result = strings.ReplaceAll(result, "{{.Value}}", fmt.Sprintf("%.2f", metric.Value))
	
	return result
}

// interpolateAnnotations replaces template variables in annotations
func (am *AlertManager) interpolateAnnotations(annotations map[string]string, metric metrics.MetricPoint) map[string]string {
	result := make(map[string]string)
	
	for k, v := range annotations {
		result[k] = am.interpolateTemplate(v, metric)
	}
	
	return result
}

// GetActiveAlerts returns all currently active alerts
func (am *AlertManager) GetActiveAlerts() []Alert {
	alerts := make([]Alert, 0, len(am.activeAlerts))
	
	for _, alert := range am.activeAlerts {
		if alert.Status == StatusFiring {
			alerts = append(alerts, *alert)
		}
	}
	
	return alerts
}

// GetAllAlerts returns all alerts (active and resolved)
func (am *AlertManager) GetAllAlerts() []Alert {
	alerts := make([]Alert, 0, len(am.activeAlerts))
	
	for _, alert := range am.activeAlerts {
		alerts = append(alerts, *alert)
	}
	
	return alerts
}

// SilenceAlert silences an active alert
func (am *AlertManager) SilenceAlert(alertID string, duration time.Duration) error {
	alert, exists := am.activeAlerts[alertID]
	if !exists {
		return fmt.Errorf("alert %s not found", alertID)
	}
	
	alert.Status = StatusSilenced
	alert.UpdatedAt = time.Now()
	
	// Automatically unsilence after duration
	go func() {
		time.Sleep(duration)
		if alert.Status == StatusSilenced {
			alert.Status = StatusFiring
			alert.UpdatedAt = time.Now()
		}
	}()
	
	return nil
}

// GetRules returns all configured alert rules
func (am *AlertManager) GetRules() []AlertRule {
	return am.rules
}

// UpdateRule updates an existing rule or adds it if it doesn't exist
func (am *AlertManager) UpdateRule(rule AlertRule) {
	for i, existingRule := range am.rules {
		if existingRule.Name == rule.Name {
			am.rules[i] = rule
			return
		}
	}
	
	// Rule doesn't exist, add it
	am.AddRule(rule)
}

// RemoveRule removes a rule by name
func (am *AlertManager) RemoveRule(name string) {
	for i, rule := range am.rules {
		if rule.Name == name {
			am.rules = append(am.rules[:i], am.rules[i+1:]...)
			break
		}
	}
}
