package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ConfigManager manages module configurations
type ConfigManager struct {
	shemHome string
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(shemHome string) *ConfigManager {
	return &ConfigManager{
		shemHome: shemHome,
	}
}

// ListModules returns all configured module names
func (cm *ConfigManager) ListModules() ([]string, error) {
	modulesDir := filepath.Join(cm.shemHome, "modules")

	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read modules directory: %w", err)
	}

	var modules []string
	for _, entry := range entries {
		if entry.IsDir() {
			// Verify it's a valid module by checking for required 'image' file
			imagePath := filepath.Join(modulesDir, entry.Name(), "image")
			if _, err := os.Stat(imagePath); err == nil {
				modules = append(modules, entry.Name())
			}
		}
	}

	return modules, nil
}

// NewModuleConfig creates a new module configuration accessor
func (cm *ConfigManager) NewModuleConfig(moduleName string) (*ModuleConfig, error) {
	modulePath := filepath.Join(cm.shemHome, "modules", moduleName)
	if _, err := os.Stat(modulePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("module %s does not exist", moduleName)
	}

	return &ModuleConfig{
		shemHome:   cm.shemHome,
		moduleName: moduleName,
	}, nil
}

// ModuleConfig provides access to a specific module's configuration
type ModuleConfig struct {
	shemHome   string
	moduleName string
}

// GetString returns a string configuration value with optional default
// Reads from file $SHEM_HOME/modules/[module_name]/[key]
func (mc *ModuleConfig) GetString(key string, defaultValue ...string) (string, error) {
	filePath := filepath.Join(mc.shemHome, "modules", mc.moduleName, key)
	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) && len(defaultValue) > 0 {
			return defaultValue[0], nil
		}
		return "", fmt.Errorf("failed to read %s file for module %s: %w", key, mc.moduleName, err)
	}
	return strings.TrimSpace(string(content)), nil
}

// GetInt returns an integer configuration value with optional default
func (mc *ModuleConfig) GetInt(key string, defaultValue ...int) (int, error) {
	value, err := mc.GetString(key)
	if err != nil {
		if len(defaultValue) > 0 {
			return defaultValue[0], nil
		}
		return 0, err
	}
	if value == "" && len(defaultValue) > 0 {
		return defaultValue[0], nil
	}

	intValue, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid integer value for %s: %s", key, value)
	}
	return intValue, nil
}

// GetFloat returns a float configuration value with optional default
func (mc *ModuleConfig) GetFloat(key string, defaultValue ...float64) (float64, error) {
	value, err := mc.GetString(key)
	if err != nil {
		if len(defaultValue) > 0 {
			return defaultValue[0], nil
		}
		return 0, err
	}
	if value == "" && len(defaultValue) > 0 {
		return defaultValue[0], nil
	}

	floatValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid float value for %s: %s", key, value)
	}
	return floatValue, nil
}

// GetBool returns a boolean configuration value with optional default
func (mc *ModuleConfig) GetBool(key string, defaultValue ...bool) (bool, error) {
	value, err := mc.GetString(key)
	if err != nil {
		if len(defaultValue) > 0 {
			return defaultValue[0], nil
		}
		return false, err
	}
	if value == "" && len(defaultValue) > 0 {
		return defaultValue[0], nil
	}

	boolValue, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value for %s: %s", key, value)
	}
	return boolValue, nil
}

// SetString sets a configuration value by writing to the corresponding file
func (mc *ModuleConfig) SetString(key, value string) error {
	filePath := filepath.Join(mc.shemHome, "modules", mc.moduleName, key)
	err := os.WriteFile(filePath, []byte(value), 0644)
	if err != nil {
		return fmt.Errorf("failed to write %s file for module %s: %w", key, mc.moduleName, err)
	}
	return nil
}

// GetBlacklistedVersions returns all blacklisted versions for this module as a map
func (mc *ModuleConfig) GetBlacklistedVersions() (map[string]struct{}, error) {
	blacklist := make(map[string]struct{})
	blacklistPath := filepath.Join(mc.shemHome, "modules", mc.moduleName, "blacklist")
	content, err := os.ReadFile(blacklistPath)
	if os.IsNotExist(err) {
		return blacklist, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read blacklist file for module %s: %w", mc.moduleName, err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			blacklist[line] = struct{}{}
		}
	}
	return blacklist, scanner.Err()
}

// IsVersionBlacklisted checks if a specific version is blacklisted
func (mc *ModuleConfig) IsVersionBlacklisted(version string) (bool, error) {
	blacklist, err := mc.GetBlacklistedVersions()
	if err != nil {
		return false, err
	}
	_, exists := blacklist[version]
	return exists, nil
}

// writeBlacklistFile writes the blacklist versions to file in ascending order
func (mc *ModuleConfig) writeBlacklistFile(versions map[string]struct{}) error {
	// Convert map to slice
	var versionSlice []string
	for v := range versions {
		versionSlice = append(versionSlice, v)
	}

	// Sort versions in ascending order
	sort.Slice(versionSlice, func(i, j int) bool {
		return compareVersions(versionSlice[i], versionSlice[j]) < 0
	})

	// Write to file
	blacklistPath := filepath.Join(mc.shemHome, "modules", mc.moduleName, "blacklist")
	content := strings.Join(versionSlice, "\n")
	if len(versionSlice) > 0 {
		content += "\n"
	}

	if err := os.WriteFile(blacklistPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write blacklist file for module %s: %w", mc.moduleName, err)
	}

	return nil
}

// AddToBlacklist adds a version to the module's blacklist
func (mc *ModuleConfig) AddToBlacklist(version string) error {
	blacklist, err := mc.GetBlacklistedVersions()
	if err != nil {
		return fmt.Errorf("failed to read blacklist for module %s: %w", mc.moduleName, err)
	}

	// Add the version to the blacklist
	blacklist[version] = struct{}{}

	// Write updated blacklist back to file
	return mc.writeBlacklistFile(blacklist)
}

// RemoveFromBlacklist removes a version from the module's blacklist
func (mc *ModuleConfig) RemoveFromBlacklist(version string) error {
	blacklist, err := mc.GetBlacklistedVersions()
	if err != nil {
		return fmt.Errorf("failed to read blacklist for module %s: %w", mc.moduleName, err)
	}

	// Check if version exists in blacklist
	if _, found := blacklist[version]; !found {
		return fmt.Errorf("version %s not found in blacklist for module %s", version, mc.moduleName)
	}

	// Remove the version from the map
	delete(blacklist, version)

	// Write updated blacklist back to file
	return mc.writeBlacklistFile(blacklist)
}
