package io.zwiftboard.android

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Bundle
import android.view.Gravity
import android.view.ViewGroup
import android.widget.Button
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts

class MainActivity : ComponentActivity() {
    private lateinit var leftStatus: TextView
    private lateinit var rightStatus: TextView
    private lateinit var linkStatus: TextView
    private lateinit var instruction: TextView
    private val refresh = object : Runnable {
        override fun run() {
            renderStatus()
            statusHandler.postDelayed(this, 1000)
        }
    }
    private val statusHandler = android.os.Handler(android.os.Looper.getMainLooper())

    private val permissionLauncher =
        registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { grants ->
            val bluetoothGranted = requiredBluetoothPermissions().all {
                grants[it] == true || checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED
            }
            if (bluetoothGranted) {
                startBridgeService()
            } else {
                instruction.text = getString(R.string.permission_required)
            }
        }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(createContent())
        renderStatus()
    }

    override fun onResume() {
        super.onResume()
        statusHandler.removeCallbacks(refresh)
        statusHandler.post(refresh)
    }

    override fun onPause() {
        statusHandler.removeCallbacks(refresh)
        super.onPause()
    }

    private fun createContent(): android.view.View {
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER_HORIZONTAL
            setPadding(24, 32, 24, 24)
        }
        val title = TextView(this).apply {
            text = getString(R.string.app_name)
            textSize = 26f
            gravity = Gravity.CENTER
        }
        instruction = TextView(this).apply {
            textSize = 15f
            text = getString(R.string.instructions)
            setPadding(0, 16, 0, 20)
        }
        leftStatus = makeStatus(getString(R.string.left_pod))
        rightStatus = makeStatus(getString(R.string.right_pod))
        linkStatus = makeStatus(getString(R.string.mywhoosh_link))

        val start = Button(this).apply {
            text = getString(R.string.start_bridge)
            setOnClickListener { requestPermissionsAndStart() }
        }
        val stop = Button(this).apply {
            text = getString(R.string.stop_bridge)
            setOnClickListener {
                stopService(Intent(this@MainActivity, ClickService::class.java))
                renderStatus()
            }
        }
        content.addView(title, matchWidth())
        content.addView(instruction, matchWidth())
        content.addView(leftStatus, matchWidth())
        content.addView(rightStatus, matchWidth())
        content.addView(linkStatus, matchWidth())
        content.addView(start, matchWidth())
        content.addView(stop, matchWidth())
        return ScrollView(this).apply { addView(content) }
    }

    private fun makeStatus(label: String) = TextView(this).apply {
        text = getString(R.string.status_line, label, getString(R.string.not_running))
        textSize = 17f
        setPadding(0, 8, 0, 8)
    }

    private fun matchWidth() = LinearLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.WRAP_CONTENT,
    )

    private fun requestPermissionsAndStart() {
        val required = requiredBluetoothPermissions().filter {
            checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED
        }.toMutableList()
        if (android.os.Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            required.add(Manifest.permission.POST_NOTIFICATIONS)
        }
        if (required.isEmpty()) startBridgeService() else permissionLauncher.launch(required.toTypedArray())
    }

    private fun requiredBluetoothPermissions(): List<String> =
        if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.S) {
            listOf(Manifest.permission.BLUETOOTH_SCAN, Manifest.permission.BLUETOOTH_CONNECT)
        } else {
            listOf(
                Manifest.permission.ACCESS_FINE_LOCATION,
                Manifest.permission.ACCESS_COARSE_LOCATION,
            )
        }

    private fun startBridgeService() {
        val intent = Intent(this, ClickService::class.java)
        startForegroundService(intent)
    }

    private fun renderStatus() {
        val prefs = getSharedPreferences(ClickService.PREFERENCES, MODE_PRIVATE)
        val running = prefs.getBoolean(ClickService.KEY_RUNNING, false)
        leftStatus.text = getString(
            R.string.status_line,
            getString(R.string.left_pod),
            statusValue(running, ClickService.KEY_LEFT, R.string.scanning),
        )
        rightStatus.text = getString(
            R.string.status_line,
            getString(R.string.right_pod),
            statusValue(running, ClickService.KEY_RIGHT, R.string.scanning),
        )
        linkStatus.text = getString(
            R.string.status_line,
            getString(R.string.mywhoosh_link),
            statusValue(running, ClickService.KEY_LINK, R.string.starting),
        )
    }

    private fun statusValue(running: Boolean, key: String, fallback: Int): String =
        if (running) {
            getSharedPreferences(ClickService.PREFERENCES, MODE_PRIVATE).getString(key, getString(fallback))
                ?: getString(fallback)
        } else {
            getString(R.string.not_running)
        }
}
