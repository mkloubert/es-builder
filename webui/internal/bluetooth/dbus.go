// EmuStation Builder
// Copyright (C) 2026  Marcel Joachim Kloubert
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package bluetooth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	bluezName     = "org.bluez"
	adapterIface  = "org.bluez.Adapter1"
	deviceIface   = "org.bluez.Device1"
	agentMgrIface = "org.bluez.AgentManager1"
	agentIface    = "org.bluez.Agent1"
	propsIface    = "org.freedesktop.DBus.Properties"
	objMgrIface   = "org.freedesktop.DBus.ObjectManager"
	agentPath     = dbus.ObjectPath("/emulationstation/agent")
	callTimeout   = 60 * time.Second
)

type dbusBus struct {
	conn *dbus.Conn
}

// agent auto-accepts pairing requests (capability NoInputNoOutput), but only
// while the pairing window is open; otherwise it rejects them.
type agent struct{ win PairingWindow }

func (a agent) check() *dbus.Error {
	if a.win != nil && a.win.Open() {
		return nil
	}
	return dbus.NewError("org.bluez.Error.Rejected", nil)
}

func (agent) Release() *dbus.Error { return nil }
func (agent) Cancel() *dbus.Error  { return nil }
func (a agent) AuthorizeService(dbus.ObjectPath, string) *dbus.Error {
	return a.check()
}
func (a agent) RequestAuthorization(dbus.ObjectPath) *dbus.Error { return a.check() }
func (a agent) RequestConfirmation(dbus.ObjectPath, uint32) *dbus.Error {
	return a.check()
}
func (a agent) RequestPinCode(dbus.ObjectPath) (string, *dbus.Error) {
	if err := a.check(); err != nil {
		return "", err
	}
	return "0000", nil
}
func (a agent) RequestPasskey(dbus.ObjectPath) (uint32, *dbus.Error) {
	if err := a.check(); err != nil {
		return 0, err
	}
	return 0, nil
}
func (agent) DisplayPinCode(dbus.ObjectPath, string) *dbus.Error { return nil }
func (agent) DisplayPasskey(dbus.ObjectPath, uint32, uint16) *dbus.Error {
	return nil
}

// NewDBus connects to the system bus and registers the pairing agent, which
// accepts requests only while win is open.
func NewDBus(win PairingWindow) (Bus, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, fmt.Errorf("system bus: %w", err)
	}
	b := &dbusBus{conn: conn}
	if err := conn.Export(agent{win: win}, agentPath, agentIface); err != nil {
		return nil, fmt.Errorf("agent export: %w", err)
	}
	mgr := conn.Object(bluezName, "/org/bluez")
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if err := mgr.CallWithContext(ctx, agentMgrIface+".RegisterAgent", 0, agentPath, "NoInputNoOutput").Err; err != nil {
		return nil, fmt.Errorf("register agent: %w", err)
	}
	if err := mgr.CallWithContext(ctx, agentMgrIface+".RequestDefaultAgent", 0, agentPath).Err; err != nil {
		return nil, fmt.Errorf("default agent: %w", err)
	}
	return b, nil
}

func (b *dbusBus) call(path dbus.ObjectPath, method string, args ...interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return b.conn.Object(bluezName, path).CallWithContext(ctx, method, 0, args...).Err
}

func (b *dbusBus) managed() (map[dbus.ObjectPath]map[string]map[string]dbus.Variant, error) {
	var objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	err := b.conn.Object(bluezName, "/").CallWithContext(ctx, objMgrIface+".GetManagedObjects", 0).Store(&objs)
	return objs, err
}

// adapter returns the path of the first adapter, or "" if none exists.
func (b *dbusBus) adapter() (dbus.ObjectPath, error) {
	objs, err := b.managed()
	if err != nil {
		return "", err
	}
	var best dbus.ObjectPath
	for p := range objs {
		if _, ok := objs[p][adapterIface]; ok && (best == "" || p < best) {
			best = p
		}
	}
	return best, nil
}

// devicePath finds the object path of the device with the given address by
// scanning the managed objects.
func (b *dbusBus) devicePath(addr string) (dbus.ObjectPath, error) {
	objs, err := b.managed()
	if err != nil {
		return "", err
	}
	for p, ifaces := range objs {
		props, ok := ifaces[deviceIface]
		if !ok {
			continue
		}
		if a, _ := props["Address"].Value().(string); strings.EqualFold(a, addr) {
			return p, nil
		}
	}
	return "", fmt.Errorf("device %s not found", addr)
}

func (b *dbusBus) HasAdapter() (bool, error) {
	p, err := b.adapter()
	return p != "", err
}

func (b *dbusBus) mustAdapter() (dbus.ObjectPath, error) {
	p, err := b.adapter()
	if err != nil {
		return "", err
	}
	if p == "" {
		return "", ErrNoAdapter
	}
	return p, nil
}

func (b *dbusBus) PowerOn() error {
	p, err := b.mustAdapter()
	if err != nil {
		return err
	}
	return b.call(p, propsIface+".Set", adapterIface, "Powered", dbus.MakeVariant(true))
}

func (b *dbusBus) StartDiscovery() error {
	p, err := b.mustAdapter()
	if err != nil {
		return err
	}
	return b.call(p, adapterIface+".StartDiscovery")
}

func (b *dbusBus) StopDiscovery() error {
	p, err := b.mustAdapter()
	if err != nil {
		return err
	}
	return b.call(p, adapterIface+".StopDiscovery")
}

func (b *dbusBus) Devices() ([]Device, error) {
	objs, err := b.managed()
	if err != nil {
		return nil, err
	}
	var out []Device
	for _, ifaces := range objs {
		props, ok := ifaces[deviceIface]
		if !ok {
			continue
		}
		var d Device
		d.Address, _ = props["Address"].Value().(string)
		d.Name, _ = props["Alias"].Value().(string)
		if d.Name == "" {
			d.Name, _ = props["Name"].Value().(string)
		}
		d.Icon, _ = props["Icon"].Value().(string)
		d.Paired, _ = props["Paired"].Value().(bool)
		d.Trusted, _ = props["Trusted"].Value().(bool)
		d.Connected, _ = props["Connected"].Value().(bool)
		if v, ok := props["RSSI"]; ok {
			if r, ok := v.Value().(int16); ok {
				d.RSSI, d.HasRSSI = r, true
			}
		}
		out = append(out, d)
	}
	SortDevices(out)
	return out, nil
}

func (b *dbusBus) deviceCall(addr, method string) error {
	p, err := b.devicePath(addr)
	if err != nil {
		return err
	}
	return b.call(p, deviceIface+"."+method)
}

func (b *dbusBus) Pair(addr string) error       { return b.deviceCall(addr, "Pair") }
func (b *dbusBus) Connect(addr string) error    { return b.deviceCall(addr, "Connect") }
func (b *dbusBus) Disconnect(addr string) error { return b.deviceCall(addr, "Disconnect") }

func (b *dbusBus) Trust(addr string) error {
	p, err := b.devicePath(addr)
	if err != nil {
		return err
	}
	return b.call(p, propsIface+".Set", deviceIface, "Trusted", dbus.MakeVariant(true))
}

func (b *dbusBus) Remove(addr string) error {
	dev, err := b.devicePath(addr)
	if err != nil {
		return err
	}
	ad, err := b.mustAdapter()
	if err != nil {
		return err
	}
	return b.call(ad, adapterIface+".RemoveDevice", dev)
}
