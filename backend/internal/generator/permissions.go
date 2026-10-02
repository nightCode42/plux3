// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package generator

// AndroidPermission is a `uses-permission` of the Android manifest.
type AndroidPermission struct {
	Name string
	// MaxSDK limits the permission to older Android versions; 0 for all.
	MaxSDK int
}

// UsageDescription is an iOS Info.plist usage-description key and the text
// the system shows when the app asks for the permission.
type UsageDescription struct {
	Key, Text string
}

// grant is what one device API needs on each platform.
type grant struct {
	android []AndroidPermission
	ios     []UsageDescription // %s is the app's name
}

// deviceAPIs maps every device API a plugin may request (the `deviceApi`
// enum of the document schema, SEC-080) to its Android permissions and iOS
// usage descriptions. A test checks that every member is mapped; an API
// that needs no permission maps to nothing.
var deviceAPIs = map[string]grant{
	"camera": {
		android: []AndroidPermission{{Name: "android.permission.CAMERA"}},
		ios:     []UsageDescription{{"NSCameraUsageDescription", "%s uses the camera for photos and scans you take in the app."}},
	},
	"photos": {
		android: []AndroidPermission{{Name: "android.permission.READ_MEDIA_IMAGES"}, {Name: "android.permission.READ_EXTERNAL_STORAGE", MaxSDK: 32}},
		ios:     []UsageDescription{{"NSPhotoLibraryUsageDescription", "%s opens the photos you choose."}},
	},
	"files": {}, // the system's document picker needs no permission
	"location": {
		android: []AndroidPermission{{Name: "android.permission.ACCESS_COARSE_LOCATION"}, {Name: "android.permission.ACCESS_FINE_LOCATION"}},
		ios:     []UsageDescription{{"NSLocationWhenInUseUsageDescription", "%s uses your location while you use the app."}},
	},
	"contacts": {
		android: []AndroidPermission{{Name: "android.permission.READ_CONTACTS"}},
		ios:     []UsageDescription{{"NSContactsUsageDescription", "%s reads the contacts you choose."}},
	},
	"biometrics": {
		android: []AndroidPermission{{Name: "android.permission.USE_BIOMETRIC"}},
		ios:     []UsageDescription{{"NSFaceIDUsageDescription", "%s uses Face ID to confirm it is you."}},
	},
	"notifications": {
		android: []AndroidPermission{{Name: "android.permission.POST_NOTIFICATIONS"}},
	},
	"clipboard": {},
	"share":     {},
	"haptics": {
		android: []AndroidPermission{{Name: "android.permission.VIBRATE"}},
	},
}
