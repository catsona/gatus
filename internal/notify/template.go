package notify

import (
	"bytes"
	"text/template"

	"github.com/catsona/gatus/internal/model"
)

// Render applies the documented template context without constructing JSON by hand.
func Render(source string, delivery model.Delivery) (string, error) {
	tmpl, err := template.New("notification").Option("missingkey=error").Parse(source)
	if err != nil {
		return "", err
	}

	var result bytes.Buffer
	if err := tmpl.Execute(&result, model.TemplateData{PR: delivery.PR, Account: delivery.Account, Event: delivery.Event}); err != nil {
		return "", err
	}

	return result.String(), nil
}
