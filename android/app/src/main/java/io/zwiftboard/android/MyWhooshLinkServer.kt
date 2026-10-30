package io.zwiftboard.android

import android.util.Log
import java.io.BufferedReader
import java.io.InputStreamReader
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

internal class MyWhooshLinkServer(
    private val onStateChanged: (String) -> Unit,
) {
    private val running = AtomicBoolean(false)
    private val clientLock = Any()
    private val controlWriter: ExecutorService = Executors.newSingleThreadExecutor { task ->
        Thread(task, "mywhoosh-link-writer").apply { isDaemon = true }
    }

    @Volatile
    private var serverSocket: ServerSocket? = null

    @Volatile
    private var activeClient: Socket? = null

    val isConnected: Boolean
        get() = activeClient?.isConnected == true && activeClient?.isClosed == false

    fun start() {
        if (!running.compareAndSet(false, true)) return

        Thread(::acceptClients, "mywhoosh-link-server").apply {
            isDaemon = true
            start()
        }
    }

    fun sendControl(line: String) {
        val client = activeClient ?: return
        controlWriter.execute {
            synchronized(clientLock) {
                if (activeClient !== client || client.isClosed) return@synchronized
                try {
                    client.getOutputStream().write(line.toByteArray(Charsets.UTF_8))
                    client.getOutputStream().flush()
                    Log.i(TAG, "Sent control message to MyWhoosh")
                } catch (error: Exception) {
                    Log.w(TAG, "Failed sending MyWhoosh control", error)
                    closeClient(client)
                }
            }
        }
    }

    fun stop() {
        running.set(false)
        controlWriter.shutdownNow()
        try {
            serverSocket?.close()
        } catch (_: Exception) {
            // Closing the listener wakes accept().
        }
        activeClient?.let(::closeClient)
        serverSocket = null
    }

    private fun acceptClients() {
        try {
            val listener = ServerSocket().apply {
                reuseAddress = true
                bind(InetSocketAddress(MyWhooshProtocol.PORT))
            }
            serverSocket = listener
            onStateChanged("Listening on TCP ${MyWhooshProtocol.PORT}; awaiting MyWhoosh")
            Log.i(TAG, "Link server listening on TCP ${MyWhooshProtocol.PORT}")

            while (running.get()) {
                val client = listener.accept()
                Log.i(TAG, "TCP client connected from ${client.inetAddress.hostAddress}")
                Thread({ handleClient(client) }, "mywhoosh-link-client").apply {
                    isDaemon = true
                    start()
                }
            }
        } catch (error: Exception) {
            if (running.get()) {
                Log.e(TAG, "MyWhoosh Link server failed", error)
                onStateChanged("Link server error: ${error.message ?: error.javaClass.simpleName}")
            }
        } finally {
            running.set(false)
            serverSocket = null
        }
    }

    private fun handleClient(client: Socket) {
        try {
            client.tcpNoDelay = true
            synchronized(clientLock) {
                activeClient?.let(::closeClient)
                activeClient = client
                client.getOutputStream().apply {
                    write(MyWhooshProtocol.connectedDevicesList.toByteArray(Charsets.UTF_8))
                    flush()
                }
            }
            onStateChanged("MyWhoosh connected")
            Log.i(TAG, "Sent ConnectedDevicesList handshake")

            val reader = BufferedReader(InputStreamReader(client.getInputStream(), Charsets.UTF_8))
            while (running.get()) {
                val line = reader.readLine() ?: break
                if (line.contains("RequestConnectedDevices")) {
                    Log.i(TAG, "MyWhoosh requested connected devices")
                    synchronized(clientLock) {
                        if (activeClient === client) {
                            client.getOutputStream().apply {
                                write(MyWhooshProtocol.connectedDevicesList.toByteArray(Charsets.UTF_8))
                                flush()
                            }
                        }
                    }
                }
            }
        } catch (error: Exception) {
            if (running.get()) Log.w(TAG, "MyWhoosh client ended", error)
        } finally {
            closeClient(client)
            if (running.get()) {
                Log.i(TAG, "MyWhoosh TCP connection closed")
                onStateChanged("Listening on TCP ${MyWhooshProtocol.PORT}; awaiting MyWhoosh")
            }
        }
    }

    private fun closeClient(client: Socket) {
        synchronized(clientLock) {
            if (activeClient === client) activeClient = null
            try {
                client.close()
            } catch (_: Exception) {
                // The socket may already be closed by the peer.
            }
        }
    }

    private companion object {
        const val TAG = "ZwiftBoardLink"
    }
}
