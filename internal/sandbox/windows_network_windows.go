//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsRPCAuthnDefault          = 0xFFFFFFFF
	windowsFWPActionBlock           = 0x00001001
	windowsFWPActrlMatchFilter      = 0x00000001
	windowsFWPEmpty                 = 0
	windowsFWPSecurityDescriptor    = 14
	windowsFWPMatchEqual            = 0
	windowsFWPMProviderPersistent   = 0x00000001
	windowsFWPMSubLayerPersistent   = 0x00000001
	windowsFWPMFilterPersistent     = 0x00000001
	windowsFWPEFilterNotFound       = 0x80320003
	windowsFWPENotFound             = 0x80320008
	windowsFWPEAlreadyExists        = 0x80320009
	windowsWFPTransactionReadWrite  = 0
	windowsWFPTransactionWaitMillis = 0xFFFFFFFF
)

var (
	procFwpmEngineOpen0        = windows.NewLazySystemDLL("fwpuclnt.dll").NewProc("FwpmEngineOpen0")
	procFwpmEngineClose0       = windows.NewLazySystemDLL("fwpuclnt.dll").NewProc("FwpmEngineClose0")
	procFwpmTransactionBegin0  = windows.NewLazySystemDLL("fwpuclnt.dll").NewProc("FwpmTransactionBegin0")
	procFwpmTransactionCommit0 = windows.NewLazySystemDLL("fwpuclnt.dll").NewProc("FwpmTransactionCommit0")
	procFwpmTransactionAbort0  = windows.NewLazySystemDLL("fwpuclnt.dll").NewProc("FwpmTransactionAbort0")
	procFwpmProviderAdd0       = windows.NewLazySystemDLL("fwpuclnt.dll").NewProc("FwpmProviderAdd0")
	procFwpmSubLayerAdd0       = windows.NewLazySystemDLL("fwpuclnt.dll").NewProc("FwpmSubLayerAdd0")
	procFwpmFilterAdd0         = windows.NewLazySystemDLL("fwpuclnt.dll").NewProc("FwpmFilterAdd0")
	procFwpmFilterDeleteByKey0 = windows.NewLazySystemDLL("fwpuclnt.dll").NewProc("FwpmFilterDeleteByKey0")
)

var (
	windowsWFPLayerALEAuthConnectV4 = windows.GUID{Data1: 0xc38d57d1, Data2: 0x05a7, Data3: 0x4c33, Data4: [8]byte{0x90, 0x4f, 0x7f, 0xbc, 0xee, 0xe6, 0x0e, 0x82}}
	windowsWFPLayerALEAuthConnectV6 = windows.GUID{Data1: 0x4a72393b, Data2: 0x319f, Data3: 0x44bc, Data4: [8]byte{0x84, 0xc3, 0xba, 0x54, 0xdc, 0xb3, 0xb6, 0xb4}}
	windowsWFPConditionALEUserID    = windows.GUID{Data1: 0xaf043a0a, Data2: 0xb34d, Data3: 0x4f86, Data4: [8]byte{0x97, 0x9c, 0xc9, 0x03, 0x71, 0xaf, 0x6e, 0x66}}
)

type windowsWFPDisplayData0 struct {
	Name        *uint16
	Description *uint16
}

type windowsFWPByteBlob struct {
	Size uint32
	Data *byte
}

type windowsFWPValue0 struct {
	Type  uint32
	Value uintptr
}

type windowsFWPConditionValue0 struct {
	Type  uint32
	Value uintptr
}

type windowsFWPMAction0 struct {
	Type uint32
	GUID windows.GUID
}

type windowsFWPMSession0 struct {
	SessionKey             windows.GUID
	DisplayData            windowsWFPDisplayData0
	Flags                  uint32
	TransactionWaitTimeout uint32
	ProcessID              uint32
	SID                    *windows.SID
	Username               *uint16
	KernelMode             int32
}

type windowsFWPMProvider0 struct {
	ProviderKey  windows.GUID
	DisplayData  windowsWFPDisplayData0
	Flags        uint32
	ProviderData windowsFWPByteBlob
	ServiceName  *uint16
}

type windowsFWPMSubLayer0 struct {
	SubLayerKey  windows.GUID
	DisplayData  windowsWFPDisplayData0
	Flags        uint32
	ProviderKey  *windows.GUID
	ProviderData windowsFWPByteBlob
	Weight       uint16
}

type windowsFWPMFilterCondition0 struct {
	FieldKey       windows.GUID
	MatchType      uint32
	ConditionValue windowsFWPConditionValue0
}

type windowsFWPMFilter0 struct {
	FilterKey           windows.GUID
	DisplayData         windowsWFPDisplayData0
	Flags               uint32
	ProviderKey         *windows.GUID
	ProviderData        windowsFWPByteBlob
	LayerKey            windows.GUID
	SubLayerKey         windows.GUID
	Weight              windowsFWPValue0
	NumFilterConditions uint32
	FilterCondition     *windowsFWPMFilterCondition0
	Action              windowsFWPMAction0
	RawContext          uint64
	Reserved            *windows.GUID
	FilterID            uint64
	EffectiveWeight     windowsFWPValue0
}

type windowsWFPUserCondition struct {
	SIDs               []*windows.SID
	SecurityDescriptor *windows.SECURITY_DESCRIPTOR
	Blob               windowsFWPByteBlob
}

func applyWindowsNetworkPlan(plan WindowsNetworkPlan) error {
	if plan.Mode != NetworkAllow && plan.Mode != NetworkDeny {
		return fmt.Errorf("%w for mode %q", ErrWindowsNetworkEnforcementUnavailable, plan.Mode)
	}
	engine, err := openWindowsWFPEngine()
	if err != nil {
		return err
	}
	defer closeWindowsWFPEngine(engine)

	if err := beginWindowsWFPTransaction(engine); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			abortWindowsWFPTransaction(engine)
		}
	}()

	providerKey, err := windowsGUIDFromString(plan.ProviderKey)
	if err != nil {
		return err
	}
	subLayerKey, err := windowsGUIDFromString(plan.SubLayerKey)
	if err != nil {
		return err
	}
	if err := ensureWindowsWFPProvider(engine, providerKey); err != nil {
		return err
	}
	if err := ensureWindowsWFPSubLayer(engine, providerKey, subLayerKey); err != nil {
		return err
	}
	if err := deleteWindowsWFPFilters(engine, windowsDenyWFPFilterSpecs()); err != nil {
		return err
	}
	if plan.Mode == NetworkDeny {
		userCondition, err := newWindowsWFPUserCondition(plan.IdentitySIDs)
		if err != nil {
			return err
		}
		if err := addWindowsWFPFilters(engine, providerKey, subLayerKey, plan.Filters, userCondition); err != nil {
			return err
		}
	}
	if err := commitWindowsWFPTransaction(engine); err != nil {
		return err
	}
	committed = true
	return nil
}

func openWindowsWFPEngine() (windows.Handle, error) {
	sessionName, err := windows.UTF16PtrFromString("Zero Windows Sandbox WFP")
	if err != nil {
		return 0, err
	}
	session := windowsFWPMSession0{
		DisplayData:            windowsWFPDisplayData0{Name: sessionName},
		TransactionWaitTimeout: windowsWFPTransactionWaitMillis,
	}
	var handle windows.Handle
	result, _, _ := procFwpmEngineOpen0.Call(
		0,
		uintptr(windowsRPCAuthnDefault),
		0,
		uintptr(unsafe.Pointer(&session)),
		uintptr(unsafe.Pointer(&handle)),
	)
	if err := windowsWFPResultError(uint32(result), "FwpmEngineOpen0"); err != nil {
		return 0, err
	}
	return handle, nil
}

func closeWindowsWFPEngine(engine windows.Handle) {
	_, _, _ = procFwpmEngineClose0.Call(uintptr(engine))
}

func beginWindowsWFPTransaction(engine windows.Handle) error {
	result, _, _ := procFwpmTransactionBegin0.Call(uintptr(engine), windowsWFPTransactionReadWrite)
	return windowsWFPResultError(uint32(result), "FwpmTransactionBegin0")
}

func commitWindowsWFPTransaction(engine windows.Handle) error {
	result, _, _ := procFwpmTransactionCommit0.Call(uintptr(engine))
	return windowsWFPResultError(uint32(result), "FwpmTransactionCommit0")
}

func abortWindowsWFPTransaction(engine windows.Handle) {
	_, _, _ = procFwpmTransactionAbort0.Call(uintptr(engine))
}

func ensureWindowsWFPProvider(engine windows.Handle, providerKey windows.GUID) error {
	name, err := windows.UTF16PtrFromString("Zero Windows Sandbox WFP")
	if err != nil {
		return err
	}
	description, err := windows.UTF16PtrFromString("Persistent WFP provider for Zero Windows sandbox filters")
	if err != nil {
		return err
	}
	provider := windowsFWPMProvider0{
		ProviderKey: providerKey,
		DisplayData: windowsWFPDisplayData0{
			Name:        name,
			Description: description,
		},
		Flags: windowsFWPMProviderPersistent,
	}
	result, _, _ := procFwpmProviderAdd0.Call(uintptr(engine), uintptr(unsafe.Pointer(&provider)), 0)
	return windowsWFPResultErrorAllowed(uint32(result), "FwpmProviderAdd0", windowsFWPEAlreadyExists)
}

func ensureWindowsWFPSubLayer(engine windows.Handle, providerKey windows.GUID, subLayerKey windows.GUID) error {
	name, err := windows.UTF16PtrFromString("Zero Windows Sandbox WFP")
	if err != nil {
		return err
	}
	description, err := windows.UTF16PtrFromString("Persistent WFP sublayer for Zero Windows sandbox filters")
	if err != nil {
		return err
	}
	subLayer := windowsFWPMSubLayer0{
		SubLayerKey: subLayerKey,
		DisplayData: windowsWFPDisplayData0{
			Name:        name,
			Description: description,
		},
		Flags:        windowsFWPMSubLayerPersistent,
		ProviderKey:  &providerKey,
		ProviderData: windowsFWPByteBlob{},
		Weight:       0x8000,
	}
	result, _, _ := procFwpmSubLayerAdd0.Call(uintptr(engine), uintptr(unsafe.Pointer(&subLayer)), 0)
	return windowsWFPResultErrorAllowed(uint32(result), "FwpmSubLayerAdd0", windowsFWPEAlreadyExists)
}

func deleteWindowsWFPFilters(engine windows.Handle, filters []WindowsWFPFilterSpec) error {
	for _, spec := range filters {
		key, err := windowsGUIDFromString(spec.Key)
		if err != nil {
			return err
		}
		result, _, _ := procFwpmFilterDeleteByKey0.Call(uintptr(engine), uintptr(unsafe.Pointer(&key)))
		if err := windowsWFPResultErrorAllowed(uint32(result), "FwpmFilterDeleteByKey0", windowsFWPEFilterNotFound, windowsFWPENotFound); err != nil {
			return err
		}
	}
	return nil
}

func addWindowsWFPFilters(engine windows.Handle, providerKey windows.GUID, subLayerKey windows.GUID, filters []WindowsWFPFilterSpec, userCondition *windowsWFPUserCondition) error {
	for _, spec := range filters {
		key, err := windowsGUIDFromString(spec.Key)
		if err != nil {
			return err
		}
		layerKey, err := windowsWFPFilterLayerGUID(spec)
		if err != nil {
			return err
		}
		name, err := windows.UTF16PtrFromString(spec.Name)
		if err != nil {
			return err
		}
		description, err := windows.UTF16PtrFromString("Block sandbox identity outbound connections")
		if err != nil {
			return err
		}
		conditions := []windowsFWPMFilterCondition0{{
			FieldKey:  windowsWFPConditionALEUserID,
			MatchType: windowsFWPMatchEqual,
			ConditionValue: windowsFWPConditionValue0{
				Type:  windowsFWPSecurityDescriptor,
				Value: uintptr(unsafe.Pointer(&userCondition.Blob)),
			},
		}}
		filter := windowsFWPMFilter0{
			FilterKey:           key,
			DisplayData:         windowsWFPDisplayData0{Name: name, Description: description},
			Flags:               windowsFWPMFilterPersistent,
			ProviderKey:         &providerKey,
			LayerKey:            layerKey,
			SubLayerKey:         subLayerKey,
			Weight:              windowsFWPValue0{Type: windowsFWPEmpty},
			NumFilterConditions: uint32(len(conditions)),
			FilterCondition:     &conditions[0],
			Action: windowsFWPMAction0{
				Type: windowsFWPActionBlock,
			},
			EffectiveWeight: windowsFWPValue0{Type: windowsFWPEmpty},
		}
		var filterID uint64
		result, _, _ := procFwpmFilterAdd0.Call(
			uintptr(engine),
			uintptr(unsafe.Pointer(&filter)),
			0,
			uintptr(unsafe.Pointer(&filterID)),
		)
		runtime.KeepAlive(userCondition)
		runtime.KeepAlive(conditions)
		if err := windowsWFPResultError(uint32(result), "FwpmFilterAdd0("+spec.Name+")"); err != nil {
			return err
		}
	}
	return nil
}

func newWindowsWFPUserCondition(identitySIDs []string) (*windowsWFPUserCondition, error) {
	identitySIDs = canonicalWindowsNetworkSIDs(identitySIDs)
	if len(identitySIDs) == 0 {
		return nil, errors.New("windows WFP user condition requires at least one identity SID")
	}
	sids := make([]*windows.SID, 0, len(identitySIDs))
	accessEntries := make([]windows.EXPLICIT_ACCESS, 0, len(identitySIDs))
	for _, value := range identitySIDs {
		sid, err := windows.StringToSid(value)
		if err != nil {
			return nil, fmt.Errorf("parse windows network identity SID %q: %w", value, err)
		}
		sids = append(sids, sid)
		accessEntries = append(accessEntries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.ACCESS_MASK(windowsFWPActrlMatchFilter),
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	descriptor, err := windows.BuildSecurityDescriptor(nil, nil, accessEntries, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("build windows WFP user security descriptor: %w", err)
	}
	return &windowsWFPUserCondition{
		SIDs:               sids,
		SecurityDescriptor: descriptor,
		Blob: windowsFWPByteBlob{
			Size: descriptor.Length(),
			Data: (*byte)(unsafe.Pointer(descriptor)),
		},
	}, nil
}

func windowsWFPFilterLayerGUID(spec WindowsWFPFilterSpec) (windows.GUID, error) {
	switch strings.TrimSpace(spec.Layer) {
	case "ale-auth-connect-v4":
		return windowsWFPLayerALEAuthConnectV4, nil
	case "ale-auth-connect-v6":
		return windowsWFPLayerALEAuthConnectV6, nil
	default:
		return windows.GUID{}, fmt.Errorf("unsupported windows WFP filter layer %q", spec.Layer)
	}
}

func windowsGUIDFromString(value string) (windows.GUID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return windows.GUID{}, errors.New("missing Windows GUID")
	}
	if !strings.HasPrefix(value, "{") {
		value = "{" + value + "}"
	}
	guid, err := windows.GUIDFromString(value)
	if err != nil {
		return windows.GUID{}, fmt.Errorf("parse Windows GUID %q: %w", value, err)
	}
	return guid, nil
}

func windowsWFPResultError(result uint32, operation string) error {
	return windowsWFPResultErrorAllowed(result, operation)
}

func windowsWFPResultErrorAllowed(result uint32, operation string, allowed ...uint32) error {
	if result == 0 {
		return nil
	}
	for _, allowedResult := range allowed {
		if result == allowedResult {
			return nil
		}
	}
	return fmt.Errorf("%s failed: 0x%08X", operation, result)
}
