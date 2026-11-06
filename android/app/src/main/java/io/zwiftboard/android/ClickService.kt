package io.zwiftboard.android

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.bluetooth.BluetoothDevice
import android.bluetooth.BluetoothGatt
import android.bluetooth.BluetoothGattCallback
import android.bluetooth.BluetoothGattCharacteristic
import android.bluetooth.BluetoothGattDescriptor
import android.bluetooth.BluetoothManager
import android.bluetooth.BluetoothProfile
import android.bluetooth.BluetoothStatusCodes
import android.bluetooth.le.ScanCallback
import android.bluetooth.le.ScanResult
import android.bluetooth.le.ScanSettings
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors

class ClickService : Service() {
    private data class PodSession(
        val gatt: BluetoothGatt,
        val side: Int,
        var async: BluetoothGattCharacteristic? = null,
        var syncRx: BluetoothGattCharacteristic? = null,
        var previousBitmap: Long = 0xFFFFFFFFL,
        var lastActivityAt: Long = SystemClock.elapsedRealtime(),
        var ready: Boolean = false,
    )

    private val mainHandler = Handler(Looper.getMainLooper())
    private val connector = Executors.newSingleThreadExecutor()
    private val sessions = ConcurrentHashMap<String, PodSession>()
    private val connecting = ConcurrentHashMap.newKeySet<String>()
    private val knownSides = ConcurrentHashMap<String, Int>()
    private val retryAfter = ConcurrentHashMap<String, Long>()
    private val sideSeenAt = ConcurrentHashMap<Int, Long>()
    private var scanner: android.bluetooth.le.BluetoothLeScanner? = null
    private var scanning = false
    private var linkServer: MyWhooshLinkServer? = null
    private var serviceRunning = false
    private var bluetoothReceiverRegistered = false

    private val bluetoothStateReceiver = object : android.content.BroadcastReceiver() {
        override fun onReceive(context: android.content.Context?, intent: Intent?) {
            if (intent?.action != android.bluetooth.BluetoothAdapter.ACTION_STATE_CHANGED) return
            val state = intent.getIntExtra(
                android.bluetooth.BluetoothAdapter.EXTRA_STATE,
                android.bluetooth.BluetoothAdapter.ERROR,
            )
            if (state == android.bluetooth.BluetoothAdapter.STATE_ON) {
                startScanning()
            } else if (state == android.bluetooth.BluetoothAdapter.STATE_TURNING_OFF ||
                state == android.bluetooth.BluetoothAdapter.STATE_OFF
            ) {
                scanning = false
                updateStatus(left = "Bluetooth off", right = "Bluetooth off")
            }
        }
    }

    private val keepalive = object : Runnable {
        override fun run() {
            if (!serviceRunning) return
            val now = SystemClock.elapsedRealtime()
            sessions.values.forEach { session ->
                if (!session.ready) return@forEach
                val idleFor = now - session.lastActivityAt
                val payload = if (idleFor >= IDLE_RESET_MS) {
                    session.lastActivityAt = now
                    ClickProtocol.idleReset
                } else {
                    ClickProtocol.keepalive
                }
                write(session, payload)
            }
            mainHandler.postDelayed(this, KEEPALIVE_INTERVAL_MS)
        }
    }

    private val scanCallback = object : ScanCallback() {
        override fun onScanResult(callbackType: Int, result: ScanResult) {
            val manufacturer = result.scanRecord?.getManufacturerSpecificData(ClickProtocol.COMPANY_ID)
                ?: return
            if (manufacturer.isEmpty()) return

            val side = manufacturer[0].toInt() and 0xFF
            if (side != ClickProtocol.LEFT_DEVICE_ID && side != ClickProtocol.RIGHT_DEVICE_ID) return

            val address = result.device.address
            knownSides[address] = side
            sideSeenAt[side] = SystemClock.elapsedRealtime()
            connectIfNeeded(result.device, side)
            publishState()
        }

        override fun onScanFailed(errorCode: Int) {
            Log.e(TAG, "BLE scan failed: $errorCode")
            scanning = false
            updateStatus(left = "Scan failed ($errorCode); retrying", right = "Scan failed ($errorCode); retrying")
            mainHandler.postDelayed({ startScanning() }, SCAN_RETRY_MS)
        }
    }

    private val gattCallback = object : BluetoothGattCallback() {
        override fun onConnectionStateChange(gatt: BluetoothGatt, status: Int, newState: Int) {
            mainHandler.post { handleConnectionState(gatt, status, newState) }
        }

        override fun onServicesDiscovered(gatt: BluetoothGatt, status: Int) {
            mainHandler.post { handleServicesDiscovered(gatt, status) }
        }

        override fun onDescriptorWrite(
            gatt: BluetoothGatt,
            descriptor: BluetoothGattDescriptor,
            status: Int,
        ) {
            if (descriptor.uuid == ClickProtocol.clientConfigurationUuid) {
                mainHandler.post {
                    if (status == BluetoothGatt.GATT_SUCCESS) {
                        startHandshake(gatt)
                    } else {
                        val session = sessions[gatt.device.address]
                        if (session != null) {
                            endSession(gatt.device.address, session, "Notification subscription failed ($status)")
                        }
                    }
                }
            }
        }

        @Deprecated("Deprecated in Android 13; kept for older Android versions")
        @Suppress("DEPRECATION")
        override fun onCharacteristicChanged(
            gatt: BluetoothGatt,
            characteristic: BluetoothGattCharacteristic,
        ) {
            val value = characteristic.value?.copyOf() ?: return
            handleNotification(gatt, characteristic.uuid, value)
        }

        override fun onCharacteristicChanged(
            gatt: BluetoothGatt,
            characteristic: BluetoothGattCharacteristic,
            value: ByteArray,
        ) {
            handleNotification(gatt, characteristic.uuid, value.copyOf())
        }
    }

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
        serviceRunning = true
        startForeground(NOTIFICATION_ID, buildNotification("Starting BLE and Link server"))
        setRunning(true)

        linkServer = MyWhooshLinkServer { state ->
            mainHandler.post { updateStatus(link = state) }
        }.also { it.start() }

        val bluetoothFilter =
            android.content.IntentFilter(android.bluetooth.BluetoothAdapter.ACTION_STATE_CHANGED)
        if (Build.VERSION.SDK_INT >= 33) {
            registerReceiver(bluetoothStateReceiver, bluetoothFilter, RECEIVER_NOT_EXPORTED)
        } else {
            @Suppress("DEPRECATION")
            registerReceiver(bluetoothStateReceiver, bluetoothFilter)
        }
        bluetoothReceiverRegistered = true
        mainHandler.postDelayed(keepalive, KEEPALIVE_INTERVAL_MS)
        startScanning()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopSelf()
            return START_NOT_STICKY
        }
        return START_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        serviceRunning = false
        mainHandler.removeCallbacks(keepalive)
        if (bluetoothReceiverRegistered) {
            unregisterReceiver(bluetoothStateReceiver)
            bluetoothReceiverRegistered = false
        }
        stopScanning()
        linkServer?.stop()
        linkServer = null
        sessions.values.forEach { session ->
            try {
                session.gatt.disconnect()
                session.gatt.close()
            } catch (error: SecurityException) {
                Log.w(TAG, "Bluetooth permission was revoked during shutdown", error)
            }
        }
        sessions.clear()
        setRunning(false)
        connector.shutdownNow()
        super.onDestroy()
    }

    private fun startScanning() {
        if (!serviceRunning || !hasBluetoothPermissions()) return
        val adapter = getSystemService(BluetoothManager::class.java)?.adapter
        if (adapter == null || !adapter.isEnabled) {
            updateStatus(left = "Bluetooth off", right = "Bluetooth off")
            mainHandler.postDelayed({ startScanning() }, SCAN_RETRY_MS)
            return
        }

        try {
            val activeScanner = adapter.bluetoothLeScanner
            if (activeScanner == null) {
                updateStatus(left = "Scanner unavailable", right = "Scanner unavailable")
                mainHandler.postDelayed({ startScanning() }, SCAN_RETRY_MS)
                return
            }
            scanner = activeScanner
            if (!scanning) {
                activeScanner.startScan(
                    null,
                    ScanSettings.Builder().setScanMode(ScanSettings.SCAN_MODE_LOW_LATENCY).build(),
                    scanCallback,
                )
                scanning = true
            }
            publishState()
        } catch (error: SecurityException) {
            Log.e(TAG, "Missing permission to scan for Click pods", error)
            updateStatus(left = "Bluetooth permission required", right = "Bluetooth permission required")
        } catch (error: RuntimeException) {
            Log.e(TAG, "Could not start Bluetooth scan", error)
            updateStatus(left = "Scan error; retrying", right = "Scan error; retrying")
            mainHandler.postDelayed({ startScanning() }, SCAN_RETRY_MS)
        }
    }

    private fun stopScanning() {
        if (!scanning) return
        try {
            scanner?.stopScan(scanCallback)
        } catch (error: SecurityException) {
            Log.w(TAG, "Bluetooth scan permission unavailable while stopping", error)
        } catch (error: RuntimeException) {
            Log.w(TAG, "Failed to stop Bluetooth scan", error)
        }
        scanning = false
    }

    private fun connectIfNeeded(device: BluetoothDevice, side: Int) {
        val address = device.address
        val now = SystemClock.elapsedRealtime()
        if (sessions.containsKey(address) || connecting.contains(address)) return
        if ((retryAfter[address] ?: 0L) > now) return
        if (!connecting.add(address)) return

        connector.execute {
            try {
                if (!hasBluetoothPermissions()) {
                    connecting.remove(address)
                    return@execute
                }
                val gatt = device.connectGatt(this, false, gattCallback, BluetoothDevice.TRANSPORT_LE)
                if (gatt == null) {
                    retryAfter[address] = SystemClock.elapsedRealtime() + CONNECT_RETRY_MS
                    connecting.remove(address)
                    return@execute
                }
                sessions[address] = PodSession(gatt, side)
                publishState()
            } catch (error: SecurityException) {
                Log.e(TAG, "Missing permission to connect to Click pod", error)
                retryAfter[address] = SystemClock.elapsedRealtime() + CONNECT_RETRY_MS
                connecting.remove(address)
            } catch (error: RuntimeException) {
                Log.e(TAG, "Could not connect to Click pod $address", error)
                retryAfter[address] = SystemClock.elapsedRealtime() + CONNECT_RETRY_MS
                connecting.remove(address)
            }
        }
    }

    private fun handleConnectionState(gatt: BluetoothGatt, status: Int, newState: Int) {
        val address = gatt.device.address
        val session = sessions[address]
        if (session == null || session.gatt !== gatt) {
            mainHandler.postDelayed({ handleConnectionState(gatt, status, newState) }, 100)
            return
        }

        if (status == BluetoothGatt.GATT_SUCCESS && newState == BluetoothProfile.STATE_CONNECTED) {
            connecting.remove(address)
            retryAfter.remove(address)
            try {
                if (!gatt.discoverServices()) {
                    endSession(address, session, "Service discovery could not start")
                }
            } catch (error: SecurityException) {
                endSession(address, session, "Bluetooth permission required")
            }
            publishState()
            return
        }

        if (newState == BluetoothProfile.STATE_DISCONNECTED || status != BluetoothGatt.GATT_SUCCESS) {
            endSession(address, session, "Disconnected")
        }
    }

    private fun handleServicesDiscovered(gatt: BluetoothGatt, status: Int) {
        val session = sessions[gatt.device.address] ?: return
        if (session.gatt !== gatt) return
        if (status != BluetoothGatt.GATT_SUCCESS) {
            endSession(gatt.device.address, session, "GATT discovery failed ($status)")
            return
        }

        val characteristics = gatt.services.flatMap { it.characteristics }
        session.async = characteristics.firstOrNull { it.uuid == ClickProtocol.asyncUuid }
        session.syncRx = characteristics.firstOrNull { it.uuid == ClickProtocol.syncRxUuid }
        if (session.async == null || session.syncRx == null) {
            endSession(gatt.device.address, session, "Click service characteristics missing")
            return
        }

        try {
            val async = session.async ?: return
            gatt.setCharacteristicNotification(async, true)
            val descriptor = async.getDescriptor(ClickProtocol.clientConfigurationUuid)
            if (descriptor == null) {
                startHandshake(gatt)
            } else if (Build.VERSION.SDK_INT >= 33) {
                val value = notificationValue(async)
                val result = gatt.writeDescriptor(
                    descriptor,
                    value,
                )
                if (result != BluetoothStatusCodes.SUCCESS) {
                    endSession(gatt.device.address, session, "Could not enable Click notifications ($result)")
                }
            } else {
                @Suppress("DEPRECATION")
                descriptor.value = notificationValue(async)
                @Suppress("DEPRECATION")
                if (!gatt.writeDescriptor(descriptor)) {
                    endSession(gatt.device.address, session, "Could not enable Click notifications")
                }
            }

        } catch (error: SecurityException) {
            endSession(gatt.device.address, session, "Bluetooth permission required")
        }
    }

    private fun notificationValue(characteristic: BluetoothGattCharacteristic): ByteArray =
        if ((characteristic.properties and BluetoothGattCharacteristic.PROPERTY_INDICATE) != 0) {
            BluetoothGattDescriptor.ENABLE_INDICATION_VALUE
        } else {
            BluetoothGattDescriptor.ENABLE_NOTIFICATION_VALUE
        }

    private fun startHandshake(gatt: BluetoothGatt) {
        val session = sessions[gatt.device.address] ?: return
        if (session.gatt !== gatt || session.ready) return
        ClickProtocol.handshakeSteps.forEachIndexed { index, payload ->
            mainHandler.postDelayed({
                if (sessions[gatt.device.address] === session) write(session, payload)
            }, index * 100L)
        }
        session.ready = true
        session.lastActivityAt = SystemClock.elapsedRealtime()
        publishState()
    }

    private fun handleNotification(gatt: BluetoothGatt, uuid: UUID, value: ByteArray) {
        mainHandler.post {
            val session = sessions[gatt.device.address] ?: return@post
            if (session.gatt !== gatt || uuid != ClickProtocol.asyncUuid || value.isEmpty()) return@post
            session.lastActivityAt = SystemClock.elapsedRealtime()

            if (value.size > 1 &&
                (value[0].toInt() and 0xFF) == 0xFF &&
                (value[1].toInt() and 0xFF) == 0x03
            ) {
                if (session.syncRx != null) write(session, ClickProtocol.challengeResponse)
                return@post
            }

            if (session.side != ClickProtocol.RIGHT_DEVICE_ID) return@post
            val current = ClickProtocol.decodeButtonBitmap(value) ?: return@post
            ClickProtocol.edges(session.previousBitmap, current).forEach { edge ->
                val line = MyWhooshProtocol.controlLine(edge.button, edge.pressed)
                Log.i(
                    TAG,
                    "RIGHT button ${edge.button} ${if (edge.pressed) "pressed" else "released"}; mapped=${line != null}; Link connected=${linkServer?.isConnected == true}",
                )
                line?.let {
                    linkServer?.sendControl(it)
                }
            }
            session.previousBitmap = current
        }
    }

    private fun write(session: PodSession, payload: ByteArray) {
        val characteristic = session.syncRx ?: return
        try {
            if (Build.VERSION.SDK_INT >= 33) {
                val result = session.gatt.writeCharacteristic(
                    characteristic,
                    payload,
                    BluetoothGattCharacteristic.WRITE_TYPE_NO_RESPONSE,
                )
                if (result != BluetoothStatusCodes.SUCCESS) {
                    Log.w(TAG, "Click write rejected (${result})")
                }
            } else {
                @Suppress("DEPRECATION")
                characteristic.writeType = BluetoothGattCharacteristic.WRITE_TYPE_NO_RESPONSE
                @Suppress("DEPRECATION")
                characteristic.value = payload
                @Suppress("DEPRECATION")
                if (!session.gatt.writeCharacteristic(characteristic)) {
                    Log.w(TAG, "Click write rejected")
                }
            }
        } catch (error: SecurityException) {
            Log.w(TAG, "Bluetooth permission unavailable during Click write", error)
        } catch (error: RuntimeException) {
            Log.w(TAG, "Click write failed", error)
        }
    }

    private fun endSession(address: String, session: PodSession, reason: String) {
        if (!sessions.remove(address, session)) return
        connecting.remove(address)
        retryAfter[address] = SystemClock.elapsedRealtime() + CONNECT_RETRY_MS
        if (session.side == ClickProtocol.RIGHT_DEVICE_ID) {
            MyWhooshProtocol.controlLine("A", false)?.let { linkServer?.sendControl(it) }
        }
        try {
            session.gatt.close()
        } catch (error: SecurityException) {
            Log.w(TAG, "Bluetooth permission unavailable while closing GATT", error)
        }
        Log.i(TAG, "${sideName(session.side)} pod $reason")
        publishState()
    }

    private fun publishState() {
        val now = SystemClock.elapsedRealtime()
        val left = podStatus(ClickProtocol.LEFT_DEVICE_ID, now)
        val right = podStatus(ClickProtocol.RIGHT_DEVICE_ID, now)
        updateStatus(left = left, right = right)
    }

    private fun podStatus(side: Int, now: Long): String {
        val session = sessions.values.firstOrNull { it.side == side }
        if (session != null) return if (session.ready) "Connected" else "Connecting"
        val lastSeen = sideSeenAt[side]
        return if (lastSeen != null && now - lastSeen < SEEN_WINDOW_MS) "Detected; connecting" else "Scanning"
    }

    private fun updateStatus(
        left: String? = null,
        right: String? = null,
        link: String? = null,
    ) {
        val prefs = getSharedPreferences(PREFERENCES, MODE_PRIVATE)
        val edit = prefs.edit()
        if (left != null) edit.putString(KEY_LEFT, left)
        if (right != null) edit.putString(KEY_RIGHT, right)
        if (link != null) edit.putString(KEY_LINK, link)
        edit.apply()
        try {
            getSystemService(NotificationManager::class.java)
                ?.notify(NOTIFICATION_ID, buildNotification(link ?: prefs.getString(KEY_LINK, "Running")))
        } catch (error: RuntimeException) {
            Log.w(TAG, "Could not update foreground notification", error)
        }
    }

    private fun setRunning(running: Boolean) {
        val edit = getSharedPreferences(PREFERENCES, MODE_PRIVATE)
            .edit()
            .putBoolean(KEY_RUNNING, running)
        if (!running) {
            edit.putString(KEY_LEFT, "not running")
                .putString(KEY_RIGHT, "not running")
                .putString(KEY_LINK, "not running")
        }
        edit.apply()
    }

    private fun buildNotification(status: String?): Notification {
        val openIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        return Notification.Builder(this, CHANNEL_ID)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(status ?: getString(R.string.bridge_running))
            .setSmallIcon(R.drawable.ic_zwiftboard)
            .setContentIntent(openIntent)
            .setOngoing(true)
            .build()
    }

    private fun createNotificationChannel() {
        val channel = NotificationChannel(
            CHANNEL_ID,
            getString(R.string.notification_channel),
            NotificationManager.IMPORTANCE_LOW,
        )
        getSystemService(NotificationManager::class.java)?.createNotificationChannel(channel)
    }

    private fun hasBluetoothPermissions(): Boolean {
        val required = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            listOf(Manifest.permission.BLUETOOTH_SCAN, Manifest.permission.BLUETOOTH_CONNECT)
        } else {
            listOf(
                Manifest.permission.ACCESS_FINE_LOCATION,
                Manifest.permission.ACCESS_COARSE_LOCATION,
            )
        }
        return required.all { checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED }
    }

    private fun sideName(side: Int) = if (side == ClickProtocol.LEFT_DEVICE_ID) "LEFT" else "RIGHT"

    companion object {
        const val PREFERENCES = "bridge-status"
        const val KEY_RUNNING = "running"
        const val KEY_LEFT = "left"
        const val KEY_RIGHT = "right"
        const val KEY_LINK = "link"

        private const val TAG = "ZwiftBoard"
        private const val CHANNEL_ID = "zwiftboard_bridge"
        private const val NOTIFICATION_ID = 42
        private const val KEEPALIVE_INTERVAL_MS = 3_000L
        private const val IDLE_RESET_MS = 55_000L
        private const val CONNECT_RETRY_MS = 5_000L
        private const val SCAN_RETRY_MS = 5_000L
        private const val SEEN_WINDOW_MS = 30_000L
        const val ACTION_STOP = "io.zwiftboard.android.STOP"
    }
}
