package com.homegrownsw.hammock.client

import java.net.InetAddress
import java.net.NetworkInterface

/**
 * Which of a paired computer's addresses to try, and in what order (the
 * iPhone's RemoteCandidates). The computer lists every address it listens on:
 * Tailscale first, then a VPN, then local networks, then extra addresses. A
 * local network's address (one inside a `lanSubnets` entry) is only tried while
 * this device has an address of its own in that subnet: away from home,
 * 192.168.1.20 is someone else. Anything else is always tried.
 */
object RemoteCandidates {
    fun ordered(addresses: List<String>, lanSubnets: List<String>, lastWorked: String?, localIPs: List<String>): List<String> {
        val all = buildList {
            if (!lastWorked.isNullOrEmpty()) add(lastWorked)
            addAll(addresses)
        }
        val subnets = lanSubnets.mapNotNull { Subnet.parse(it) }
        val mine = localIPs.mapNotNull { ipBytes(it) }
        val seen = HashSet<String>()
        return all.filter { a -> a.isNotEmpty() && seen.add(a) && reachableFromHere(a, subnets, mine) }
    }

    internal fun reachableFromHere(address: String, subnets: List<Subnet>, localIPs: List<ByteArray>): Boolean {
        val ip = ipBytes(host(address)) ?: return true // a name: always
        val inside = subnets.filter { it.contains(ip) }
        if (inside.isEmpty()) return true
        return inside.any { net -> localIPs.any { net.contains(it) } }
    }

    /** The host of "host:port" or "[v6]:port" (an address without a port as is). */
    fun host(address: String): String {
        if (address.startsWith("[")) {
            val end = address.indexOf(']')
            if (end > 0) return address.substring(1, end)
        }
        val colon = address.lastIndexOf(':')
        if (colon >= 0 && address.indexOf(':') == colon) return address.substring(0, colon)
        return address
    }

    /** The port of "host:port", or null. */
    fun port(address: String): Int? {
        val colon = address.lastIndexOf(':')
        if (colon < 0) return null
        if (!address.startsWith("[") && address.indexOf(':') != colon) return null
        return address.substring(colon + 1).toIntOrNull()
    }

    fun address(host: String, port: Int): String = if (host.contains(':')) "[$host]:$port" else "$host:$port"

    /** An IPv4 (4 bytes) or IPv6 (16) literal's bytes; null for anything else (never a DNS lookup). */
    fun ipBytes(s0: String): ByteArray? {
        val s = s0.substringBefore('%')
        if (s.isEmpty()) return null
        val v4 = s.split('.')
        if (v4.size == 4 && v4.all { p -> p.isNotEmpty() && p.length <= 3 && p.all(Char::isDigit) && p.toInt() <= 255 }) {
            return ByteArray(4) { v4[it].toInt().toByte() }
        }
        if (s.contains(':') && s.all { it.isDigit() || it in 'a'..'f' || it in 'A'..'F' || it == ':' || it == '.' }) {
            return try { InetAddress.getByName(s).address.takeIf { it.size == 16 } } catch (_: Exception) { null }
        }
        return null
    }

    class Subnet(val bytes: ByteArray, val prefix: Int) {
        fun contains(ip: ByteArray): Boolean {
            if (ip.size != bytes.size) return false
            var left = prefix
            for (i in bytes.indices) {
                if (left <= 0) break
                val bits = minOf(left, 8)
                val mask = (0xFF shl (8 - bits)) and 0xFF
                if ((ip[i].toInt() and mask) != (bytes[i].toInt() and mask)) return false
                left -= bits
            }
            return true
        }

        companion object {
            fun parse(cidr: String): Subnet? {
                val parts = cidr.split('/', limit = 2)
                if (parts.size != 2) return null
                val b = ipBytes(parts[0]) ?: return null
                val p = parts[1].toIntOrNull() ?: return null
                if (p < 0 || p > b.size * 8) return null
                return Subnet(b, p)
            }
        }
    }

    /** This device's addresses on interfaces that are up (loopback and cellular left out). */
    fun interfaceAddresses(): List<String> = interfaces().filter { !isCellular(it.first) }.map { it.second }

    /** Whether this device is on Wi‑Fi or Ethernet now: where mDNS can find the computer. */
    fun onLocalNetwork(): Boolean = interfaces().any { (name, ip) ->
        (name.startsWith("wlan") || name.startsWith("eth") || name.startsWith("en")) && !ip.contains(':') && !ip.startsWith("169.254.")
    }

    private fun isCellular(name: String) = name.startsWith("rmnet") || name.startsWith("ccmni") || name.startsWith("pdp_ip")

    private fun interfaces(): List<Pair<String, String>> = try {
        NetworkInterface.getNetworkInterfaces()?.toList().orEmpty()
            .filter { runCatching { it.isUp && !it.isLoopback }.getOrDefault(false) }
            .flatMap { ni -> ni.inetAddresses.toList().map { ni.name to (it.hostAddress ?: "").substringBefore('%') } }
            .filter { it.second.isNotEmpty() }
    } catch (_: Exception) {
        emptyList()
    }
}
