import { useState } from 'react'
import axios from 'axios'

interface CanDeviceConfig {
  name: string
  host_device: string
  container_path: string
  bitrate?: number
}

interface TapConfig {
  name: string
  host_interface: string
  container_path: string
  ip_address?: string
  netmask?: string
  enable_bridge: boolean
  bridge_interface?: string
  pasta_mode?: boolean
  tun_over_uart?: boolean
  uart_device_path?: string
  uart_baud_rate?: number
}

interface BluetoothConfig {
  enabled: boolean
  transport: 'hci' | 'hci_uart'
  hci_device: string
  hci_device_index: number
  host_device_path: string
  uart_device_path?: string
  uart_baud_rate?: number
  advertising_mode: string
}

interface UARTForwardingConfig {
  enabled: boolean
  mode: 'tun'
  host_device_path: string
  container_device_path: string
  baud_rate: number
  mtu?: number
}

interface NetworkPanelProps {
  sessionId: string
  onConfigUpdate: () => void
  embedded?: boolean
}

export default function NetworkConfigPanel({ sessionId, onConfigUpdate, embedded = false }: NetworkPanelProps) {
  const [showPanel, setShowPanel] = useState(false)
  const [canDevices, setCanDevices] = useState<CanDeviceConfig[]>([])
  const [tapInterfaces, setTapInterfaces] = useState<TapConfig[]>([])
  const [bluetoothConfig, setBluetoothConfig] = useState<BluetoothConfig | null>(null)
  const [uartForwarding, setUartForwarding] = useState<UARTForwardingConfig | null>(null)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')

  const handleSaveConfig = async () => {
    setSaving(true)
    setMessage('')
    try {
      const config: any = {}

      if (canDevices.length > 0) {
        config.can_devices = canDevices
      }
      if (tapInterfaces.length > 0) {
        config.tap_interfaces = tapInterfaces
      }
      if (bluetoothConfig) {
        config.bluetooth_config = bluetoothConfig
      }
      if (uartForwarding) {
        config.uart_forwarding = uartForwarding
      }

      await axios.patch(`/api/sessions/${sessionId}`, config)
      setMessage('Networking config updated successfully')
      onConfigUpdate()
    } catch (err: any) {
      const errorMsg = err?.response?.data?.error || 'Failed to update configuration'
      setMessage(errorMsg)
    } finally {
      setSaving(false)
    }
  }

  const addCanDevice = () => {
    setCanDevices([...canDevices, {
      name: `vcan${canDevices.length}`,
      host_device: `/dev/vcan${canDevices.length}`,
      container_path: '',
      bitrate: 500000
    }])
  }

  const removeCanDevice = (idx: number) => {
    setCanDevices(canDevices.filter((_, i) => i !== idx))
  }

  const updateCanDevice = (idx: number, field: string, value: any) => {
    const updated = [...canDevices]
      ; (updated[idx] as any)[field] = value
    setCanDevices(updated)
  }

  const addTapInterface = () => {
    setTapInterfaces([...tapInterfaces, {
      name: `tap${tapInterfaces.length}`,
      host_interface: `tap${tapInterfaces.length}`,
      container_path: '',
      ip_address: '',
      netmask: '',
      enable_bridge: false,
      bridge_interface: '',
      pasta_mode: false,
      tun_over_uart: false,
      uart_device_path: '',
      uart_baud_rate: 115200,
    }])
  }

  const removeTapInterface = (idx: number) => {
    setTapInterfaces(tapInterfaces.filter((_, i) => i !== idx))
  }

  const updateTapInterface = (idx: number, field: string, value: any) => {
    const updated = [...tapInterfaces]
      ; (updated[idx] as any)[field] = value
    setTapInterfaces(updated)
  }

  const panelVisible = embedded || showPanel

  return (
    <div className={embedded ? '' : 'bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg shadow p-6'}>
      {!embedded && (
        <div className="flex justify-between items-center mb-4">
          <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">Advanced Networking</h2>
          <button
            onClick={() => setShowPanel(!showPanel)}
            className="text-sky-700 dark:text-sky-300 hover:text-sky-900 dark:hover:text-sky-200 text-sm font-medium"
          >
            {showPanel ? 'Hide' : 'Configure'}
          </button>
        </div>
      )}

      {message && (
        <div className={`mb-4 rounded-md px-3 py-2 text-sm ${message.includes('success') || message.includes('updated')
            ? 'bg-emerald-50 dark:bg-emerald-900/40 border border-emerald-200 dark:border-emerald-700 text-emerald-900 dark:text-emerald-200'
            : 'bg-rose-50 dark:bg-rose-900/40 border border-rose-200 dark:border-rose-700 text-rose-900 dark:text-rose-200'
          }`}>
          {message}
        </div>
      )}

      {panelVisible && (
        <div className="space-y-6">
          {/* SocketCAN Configuration */}
          <div className="border-t pt-4">
            <div className="flex justify-between items-center mb-3">
              <h3 className="font-medium text-slate-900 dark:text-slate-100">SocketCAN Interfaces</h3>
              <button
                onClick={addCanDevice}
                className="text-xs bg-sky-700 dark:bg-sky-600 text-white px-2 py-1 rounded hover:bg-sky-800 dark:hover:bg-sky-500"
              >
                + Add CAN Device
              </button>
            </div>
            <div className="space-y-3">
              {canDevices.map((device, idx) => (
                <div key={idx} className="bg-slate-50 dark:bg-slate-800 p-3 rounded border border-slate-200 dark:border-slate-700">
                  <div className="grid grid-cols-2 gap-3 mb-2">
                    <input
                      type="text"
                      placeholder="Device name (e.g., vcan0)"
                      value={device.name}
                      onChange={(e) => updateCanDevice(idx, 'name', e.target.value)}
                      className="px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                    />
                    <input
                      type="text"
                      placeholder="Host device (e.g., /dev/vcan0)"
                      value={device.host_device}
                      onChange={(e) => updateCanDevice(idx, 'host_device', e.target.value)}
                      className="px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                    />
                    <input
                      type="number"
                      placeholder="Bitrate (bps)"
                      value={device.bitrate || 500000}
                      onChange={(e) => updateCanDevice(idx, 'bitrate', parseInt(e.target.value))}
                      className="px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                    />
                  </div>
                  <button
                    onClick={() => removeCanDevice(idx)}
                    className="text-xs text-rose-700 dark:text-rose-300 hover:text-rose-900 dark:hover:text-rose-200"
                  >
                    Remove
                  </button>
                </div>
              ))}
            </div>
          </div>

          {/* TAP Configuration */}
          <div className="border-t pt-4">
            <div className="flex justify-between items-center mb-3">
              <h3 className="font-medium text-slate-900 dark:text-slate-100">TAP Interfaces</h3>
              <button
                onClick={addTapInterface}
                className="text-xs bg-sky-700 dark:bg-sky-600 text-white px-2 py-1 rounded hover:bg-sky-800 dark:hover:bg-sky-500"
              >
                + Add TAP Interface
              </button>
            </div>
            <div className="space-y-3">
              {tapInterfaces.map((tap, idx) => (
                <div key={idx} className="bg-slate-50 dark:bg-slate-800 p-3 rounded border border-slate-200 dark:border-slate-700">
                  <div className="grid grid-cols-2 gap-3 mb-2">
                    <input
                      type="text"
                      placeholder="Interface name"
                      value={tap.name}
                      onChange={(e) => updateTapInterface(idx, 'name', e.target.value)}
                      className="px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                    />
                    <input
                      type="text"
                      placeholder="Host interface (e.g., tap0)"
                      value={tap.host_interface}
                      onChange={(e) => updateTapInterface(idx, 'host_interface', e.target.value)}
                      className="px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                    />
                    <input
                      type="text"
                      placeholder="IP address (optional)"
                      value={tap.ip_address || ''}
                      onChange={(e) => updateTapInterface(idx, 'ip_address', e.target.value)}
                      className="px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                    />
                    <input
                      type="text"
                      placeholder="Netmask (optional)"
                      value={tap.netmask || ''}
                      onChange={(e) => updateTapInterface(idx, 'netmask', e.target.value)}
                      className="px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                    />
                  </div>
                  <label className="flex items-center gap-2 mb-2">
                    <input
                      type="checkbox"
                      checked={tap.tun_over_uart || false}
                      onChange={(e) => {
                        if (e.target.checked) {
                          updateTapInterface(idx, 'tun_over_uart', true)
                          updateTapInterface(idx, 'enable_bridge', false)
                          updateTapInterface(idx, 'pasta_mode', false)
                        } else {
                          updateTapInterface(idx, 'tun_over_uart', false)
                        }
                      }}
                      disabled={tap.enable_bridge || tap.pasta_mode}
                      className="w-3 h-3 disabled:opacity-50"
                    />
                    <span className="text-sm text-slate-700 dark:text-slate-200">TUN over UART</span>
                  </label>
                  <label className="flex items-center gap-2 mb-2">
                    <input
                      type="checkbox"
                      checked={tap.pasta_mode || false}
                      onChange={(e) => {
                        if (e.target.checked) {
                          updateTapInterface(idx, 'pasta_mode', true)
                          updateTapInterface(idx, 'enable_bridge', false)
                        } else {
                          updateTapInterface(idx, 'pasta_mode', false)
                        }
                      }}
                      disabled={tap.enable_bridge || tap.tun_over_uart}
                      className="w-3 h-3 disabled:opacity-50"
                    />
                    <span className="text-sm text-slate-700 dark:text-slate-200">Pasta Mode (transparent forwarding)</span>
                  </label>
                  <label className="flex items-center gap-2 mb-2">
                    <input
                      type="checkbox"
                      checked={tap.enable_bridge}
                      onChange={(e) => {
                        if (e.target.checked) {
                          updateTapInterface(idx, 'enable_bridge', true)
                          updateTapInterface(idx, 'pasta_mode', false)
                        } else {
                          updateTapInterface(idx, 'enable_bridge', false)
                        }
                      }}
                      disabled={tap.pasta_mode || tap.tun_over_uart}
                      className="w-3 h-3 disabled:opacity-50"
                    />
                    <span className="text-sm text-slate-700 dark:text-slate-200">Enable bridge</span>
                  </label>
                  {tap.tun_over_uart && (
                    <>
                      <input
                        type="text"
                        placeholder="UART device path (e.g., /dev/ttyUSB1)"
                        value={tap.uart_device_path || ''}
                        onChange={(e) => updateTapInterface(idx, 'uart_device_path', e.target.value)}
                        className="w-full px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded mb-2"
                      />
                      <input
                        type="number"
                        placeholder="UART baud rate"
                        value={tap.uart_baud_rate || 115200}
                        onChange={(e) => updateTapInterface(idx, 'uart_baud_rate', parseInt(e.target.value) || 115200)}
                        className="w-full px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded mb-2"
                      />
                    </>
                  )}
                  {tap.enable_bridge && (
                    <input
                      type="text"
                      placeholder="Bridge to interface (e.g., eth0)"
                      value={tap.bridge_interface || ''}
                      onChange={(e) => updateTapInterface(idx, 'bridge_interface', e.target.value)}
                      className="w-full px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded mb-2"
                    />
                  )}
                  {(tap.pasta_mode || tap.enable_bridge || tap.tun_over_uart) && (
                    <p className="text-xs text-slate-700 dark:text-slate-200 bg-sky-50 dark:bg-sky-900/30 px-2 py-1 rounded mb-2">
                      {tap.tun_over_uart ? 'UART transport forwarding mode for network stack' : tap.pasta_mode ? 'User namespace isolation with transparent TCP/UDP forwarding' : 'Linux bridge mode with explicit network setup'}
                    </p>
                  )}
                  <button
                    onClick={() => removeTapInterface(idx)}
                    className="text-xs text-rose-700 dark:text-rose-300 hover:text-rose-900 dark:hover:text-rose-200"
                  >
                    Remove
                  </button>
                </div>
              ))}
            </div>
          </div>

          {/* Bluetooth Configuration */}
          <div className="border-t pt-4">
            <h3 className="font-medium text-slate-900 dark:text-slate-100 mb-3">Bluetooth HCI</h3>
            <label className="flex items-center gap-3 mb-3">
              <input
                type="checkbox"
                checked={bluetoothConfig?.enabled || false}
                onChange={(e) => {
                  if (e.target.checked) {
                    setBluetoothConfig({
                      enabled: true,
                      transport: 'hci',
                      hci_device: '/dev/hci0',
                      hci_device_index: 0,
                      host_device_path: '/dev/hci0',
                      advertising_mode: 'connectable'
                    })
                  } else {
                    setBluetoothConfig(null)
                  }
                }}
                className="w-4 h-4 text-sky-600"
              />
              <span className="text-sm text-slate-700 dark:text-slate-200">Enable Bluetooth HCI</span>
            </label>
            {bluetoothConfig && (
              <div className="bg-slate-50 dark:bg-slate-800 p-3 rounded border border-slate-200 dark:border-slate-700 space-y-2">
                <select
                  value={bluetoothConfig.transport}
                  onChange={(e) => {
                    const transport = e.target.value as 'hci' | 'hci_uart'
                    setBluetoothConfig({
                      ...bluetoothConfig,
                      transport,
                      host_device_path: transport === 'hci' ? (bluetoothConfig.host_device_path || '/dev/hci0') : '',
                      uart_device_path: transport === 'hci_uart' ? (bluetoothConfig.uart_device_path || '/dev/ttyUSB0') : ''
                    })
                  }}
                  className="w-full px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                >
                  <option value="hci">Native HCI Device</option>
                  <option value="hci_uart">HCI over UART</option>
                </select>
                {bluetoothConfig.transport === 'hci' ? (
                  <input
                    type="text"
                    placeholder="HCI device (e.g., /dev/hci0)"
                    value={bluetoothConfig.host_device_path}
                    onChange={(e) => setBluetoothConfig({ ...bluetoothConfig, host_device_path: e.target.value })}
                    className="w-full px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                  />
                ) : (
                  <>
                    <input
                      type="text"
                      placeholder="UART device (e.g., /dev/ttyUSB0)"
                      value={bluetoothConfig.uart_device_path || ''}
                      onChange={(e) => setBluetoothConfig({ ...bluetoothConfig, uart_device_path: e.target.value })}
                      className="w-full px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                    />
                    <input
                      type="number"
                      placeholder="UART baud rate"
                      value={bluetoothConfig.uart_baud_rate || 115200}
                      onChange={(e) => setBluetoothConfig({ ...bluetoothConfig, uart_baud_rate: parseInt(e.target.value) || 115200 })}
                      className="w-full px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                    />
                  </>
                )}
                <select
                  value={bluetoothConfig.advertising_mode}
                  onChange={(e) => setBluetoothConfig({ ...bluetoothConfig, advertising_mode: e.target.value })}
                  className="w-full px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                >
                  <option value="connectable">Connectable</option>
                  <option value="scannable">Scannable</option>
                  <option value="non_connectable">Non-Connectable</option>
                </select>
              </div>
            )}
          </div>

          {/* UART-based Network Forwarding */}
          <div className="border-t pt-4">
            <h3 className="font-medium text-slate-900 dark:text-slate-100 mb-3">UART Network Forwarding (TUN-over-UART)</h3>
            <label className="flex items-center gap-3 mb-3">
              <input
                type="checkbox"
                checked={uartForwarding?.enabled || false}
                onChange={(e) => {
                  if (e.target.checked) {
                    setUartForwarding({
                      enabled: true,
                      mode: 'tun',
                      host_device_path: '/dev/ttyUSB1',
                      container_device_path: '/dev/ttyTUN0',
                      baud_rate: 115200,
                      mtu: 1500,
                    })
                  } else {
                    setUartForwarding(null)
                  }
                }}
                className="w-4 h-4 text-sky-600"
              />
              <span className="text-sm text-slate-700 dark:text-slate-200">Enable UART transport for network stack</span>
            </label>
            {uartForwarding && (
              <div className="bg-slate-50 dark:bg-slate-800 p-3 rounded border border-slate-200 dark:border-slate-700 space-y-2">
                <input
                  type="text"
                  placeholder="Host UART device (e.g., /dev/ttyUSB1)"
                  value={uartForwarding.host_device_path}
                  onChange={(e) => setUartForwarding({ ...uartForwarding, host_device_path: e.target.value })}
                  className="w-full px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                />
                <input
                  type="text"
                  placeholder="Container UART path (e.g., /dev/ttyTUN0)"
                  value={uartForwarding.container_device_path}
                  onChange={(e) => setUartForwarding({ ...uartForwarding, container_device_path: e.target.value })}
                  className="w-full px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                />
                <input
                  type="number"
                  placeholder="Baud rate"
                  value={uartForwarding.baud_rate}
                  onChange={(e) => setUartForwarding({ ...uartForwarding, baud_rate: parseInt(e.target.value) || 115200 })}
                  className="w-full px-2 py-1 text-sm border border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 rounded"
                />
              </div>
            )}
          </div>

          {/* Save Button */}
          <div className="border-t pt-4">
            <button
              onClick={handleSaveConfig}
              disabled={saving}
              className="w-full bg-sky-700 dark:bg-sky-600 text-white py-2 px-4 rounded-lg hover:bg-sky-800 dark:hover:bg-sky-500 disabled:bg-slate-400 disabled:cursor-not-allowed"
            >
              {saving ? 'Saving...' : 'Save Network Configuration'}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
