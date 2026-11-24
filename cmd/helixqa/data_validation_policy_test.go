// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveDataValidation(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }

	off := resolveDataValidation(false, "", "", get)
	assert.False(t, off.On, "default is off")
	assert.Contains(t, off.describe(), "off")

	env["HELIX_DATA_VALIDATION"] = "1"
	env["HELIX_DATA_VALIDATION_USER_ENV"] = "APP_QA_USER"
	env["HELIX_DATA_VALIDATION_PASS_ENV"] = "APP_QA_PASS"
	on := resolveDataValidation(false, "", "", get)
	assert.True(t, on.On)
	assert.Equal(t, "APP_QA_USER", on.UserEnv)
	assert.Equal(t, "APP_QA_PASS", on.PassEnv)
	assert.Contains(t, on.describe(), "APP_QA_USER (UNSET)")

	env["APP_QA_USER"] = "u-secret-user"
	env["APP_QA_PASS"] = "p-secret-pass"
	d := on.describe()
	assert.Contains(t, d, "APP_QA_USER (set)")
	assert.NotContains(t, d, "u-secret-user", "a value is never printed")
	assert.NotContains(t, d, "p-secret-pass", "a value is never printed")

	// A flag name wins over the env-supplied name.
	assert.Equal(t, "OTHER", resolveDataValidation(true, "OTHER", "", get).UserEnv)
}

func TestAutonomous_DataValidationBannerDefaultOff(t *testing.T) {
	env, home := autonomousEnv(t, nil)
	out, _ := runAutonomousChild(t, env, "autonomous", "-project", home, "-platforms", "web",
		"-timeout", "20s", "-curiosity=false", "-output", filepath.Join(home, "out"), "-env", filepath.Join(home, "none.env"))
	assert.Contains(t, out, "Data validation:  off")
	assert.NotContains(t, out, "admin123")
}
