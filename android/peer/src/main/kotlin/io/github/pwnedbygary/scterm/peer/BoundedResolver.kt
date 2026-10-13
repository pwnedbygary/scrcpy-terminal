package io.github.pwnedbygary.scterm.peer

import java.net.InetAddress
import java.net.SocketTimeoutException
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException

/** DNS can ignore interrupts: bound caller waiting and bound resolver resource use. */
internal class BoundedResolver(private val lookup: (String) -> InetAddress = InetAddress::getByName) {
    private val workers = ThreadPoolExecutor(2, 2, 0L, TimeUnit.MILLISECONDS, ArrayBlockingQueue(8)) { task ->
        Thread(task, "peer-probe-dns").apply { isDaemon = true }
    }
    fun resolve(host: String, timeoutMs: Int): InetAddress {
        // Numeric endpoints need no resolver worker, even if platform DNS is stuck.
        if (host.matches(Regex("[0-9]{1,3}(\\.[0-9]{1,3}){3}")) || ':' in host) return InetAddress.getByName(host)
        val task = workers.submit<InetAddress> { lookup(host) }
        try {
            return task.get(timeoutMs.toLong(), TimeUnit.MILLISECONDS)
        } catch (_: TimeoutException) {
            throw SocketTimeoutException("hostname lookup timed out")
        } finally {
            task.cancel(true)
            workers.purge()
        }
    }
}
