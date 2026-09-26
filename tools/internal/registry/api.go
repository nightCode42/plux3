// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package registry

import "fmt"

// FlutterAPI is the snapshot of the pinned Flutter SDK written by
// packages/plux_widget_api (schema/widgets/flutter-api.json).
type FlutterAPI struct {
	// Flutter is the Flutter version the snapshot was taken from.
	Flutter string `json:"flutter"`
	// Classes maps "<library>#<class>" to its constructors.
	Classes map[string]FlutterClassAPI `json:"classes"`
	// Enums maps "<library>#<enum>" to its values in declaration order.
	Enums map[string][]FlutterEnumValue `json:"enums"`
}

// FlutterClassAPI holds the extracted constructors of a class.
type FlutterClassAPI struct {
	// Constructors maps a constructor name ("" for the unnamed one) to its
	// parameters in declaration order.
	Constructors map[string][]Parameter `json:"constructors"`
}

// Parameter is a constructor parameter.
type Parameter struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Required   bool   `json:"required"`
	Named      bool   `json:"named"`
	Default    string `json:"default,omitempty"`
	Deprecated bool   `json:"deprecated,omitempty"`
}

// FlutterEnumValue is a value of a Flutter enum.
type FlutterEnumValue struct {
	Name       string `json:"name"`
	Deprecated bool   `json:"deprecated,omitempty"`
}

// readAPI reads and decodes the snapshot.
func readAPI(file string) (*FlutterAPI, error) {
	api, err := decodeFile[FlutterAPI](file)
	if err != nil {
		return nil, err
	}
	if api.Flutter == "" {
		return nil, fmt.Errorf("the snapshot names no Flutter version")
	}
	return api, nil
}
