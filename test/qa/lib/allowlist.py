import copy
import ipaddress
import sh

import lib
from lib import daemon, firewall


MSG_ALLOWLIST_ADD_SUBNET_SUCCESS = "Subnet %s is allowlisted successfully."
MSG_ALLOWLIST_REMOVE_SUBNET_SUCCESS = "Subnet %s is removed from the allowlist successfully."

MSG_ALLOWLIST_ADD_PORT_SUCCESS = "Port %s (%s) is allowlisted successfully."
MSG_ALLOWLIST_REMOVE_PORT_SUCCESS = "Port %s (%s) is removed from the allowlist successfully."

MSG_ALLOWLIST_ADD_PORT_RANGE_SUCCESS = "Ports %s (%s) are allowlisted successfully."
MSG_ALLOWLIST_REMOVE_PORT_RANGE_SUCCESS = "Ports %s (%s) are removed from the allowlist successfully."


def _is_private_subnet(subnet_str):
    private_networks = [
        ipaddress.IPv4Network('10.0.0.0/8'),
        ipaddress.IPv4Network('172.16.0.0/12'),
        ipaddress.IPv4Network('192.168.0.0/16')
    ]

    try:
        subnet = ipaddress.IPv4Network(subnet_str)
        for private_net in private_networks:
            if subnet.overlaps(private_net):
                return True
        return False
    except ValueError:
        # Handle the case where an invalid subnet string is provided
        return False


def add_ports_to_allowlist(ports_list: list[lib.Port], allowlist_alias="allowlist") -> bool:
    _ports_list = copy.deepcopy(ports_list) # We don't want to alter value later in original list
    cmd = []
    cmd_message = None
    expected_message = None

    for port in _ports_list:
        if ":" in port.value:
            # Port range
            range_start, range_end = port.value.split(":")

            cmd = ["ports", range_start, range_end]
            if port.protocol != lib.Protocol.ALL:
                cmd.extend(["protocol", str(port.protocol)])

            port.value = port.value.replace(":", " - ")
            expected_message = MSG_ALLOWLIST_ADD_PORT_RANGE_SUCCESS % (port.value, port.protocol)
        else:
            # Single port
            cmd = ["port", port.value]
            if port.protocol != lib.Protocol.ALL:
                cmd.extend(["protocol", str(port.protocol)])

            expected_message = MSG_ALLOWLIST_ADD_PORT_SUCCESS % (port.value, port.protocol)

        cmd_message = sh.nordvpn(allowlist_alias, "add", cmd)
        print(cmd_message)

        assert sh.nordvpn.settings().count(f" {port.value} ({str(port.protocol)})") == 1, \
            f"Port(range) not found or found more than once in `nordvpn settings`"

        assert cmd_message != None and expected_message in cmd_message, \
            f"Wrong allowlist message.\nExpected: {expected_message}\nGot: {cmd_message}"


def remove_ports_from_allowlist(ports_list: list[lib.Port], allowlist_alias="allowlist") -> bool:
    _ports_list = copy.deepcopy(ports_list)
    cmd = []
    cmd_message = None
    expected_message = None

    for port in _ports_list:
        if ":" in port.value:
            # Port range
            range_start, range_end = port.value.split(":")

            cmd = ["ports", range_start, range_end]
            if port.protocol != lib.Protocol.ALL:
                cmd.extend(["protocol", str(port.protocol)])

            port.value = port.value.replace(":", " - ")
            expected_message = MSG_ALLOWLIST_REMOVE_PORT_RANGE_SUCCESS % (port.value, port.protocol)
        else:
            # Single port
            cmd = ["port", port.value]
            if port.protocol != lib.Protocol.ALL:
                cmd.extend(["protocol", str(port.protocol)])

            expected_message = MSG_ALLOWLIST_REMOVE_PORT_SUCCESS % (port.value, port.protocol)

        cmd_message = sh.nordvpn(allowlist_alias, "remove", cmd)
        print(cmd_message)

        assert sh.nordvpn.settings().count(f" {port.value} ({str(port.protocol)})") == 0, \
            f"Port(range) found in `nordvpn settings`"

        assert cmd_message != None and expected_message in cmd_message, \
            f"Wrong allowlist message.\nExpected: {expected_message}\nGot: {cmd_message}"


def add_subnet_to_allowlist(subnet_list: list[str], allowlist_alias="allowlist") -> bool:
    _subnet_list = copy.deepcopy(subnet_list) # We don't want to alter value later in original list

    for subnet in _subnet_list:
        cmd_message = sh.nordvpn(allowlist_alias, "add", "subnet", subnet)
        expected_message = MSG_ALLOWLIST_ADD_SUBNET_SUCCESS % subnet

        assert expected_message in cmd_message, \
            f"Wrong allowlist message.\nExpected: {expected_message}\nGot: {cmd_message}"

        assert sh.nordvpn.settings().count(subnet) == 1, \
            f"Subnet not found or found more than once in `nordvpn settings`"

        # If subnet /32 whitelisted, only IP Address is visible in `ip route`
        if "/32" in subnet:
            subnet = subnet.replace("/32", "")

        if daemon.is_connected() and not _is_private_subnet(subnet):
            assert subnet in sh.ip.route.show.table(firewall.IP_ROUTE_TABLE), \
                f"Subnet {subnet} not found in `ip route show table {firewall.IP_ROUTE_TABLE}`\n{sh.ip.route.show.table(firewall.IP_ROUTE_TABLE)}"
        else:
            assert subnet not in sh.ip.route.show.table(firewall.IP_ROUTE_TABLE), \
                f"Subnet found in `ip route show table {firewall.IP_ROUTE_TABLE}`"


def remove_subnet_from_allowlist(subnet_list: list[str], allowlist_alias="allowlist") -> bool:
    _subnet_list = copy.deepcopy(subnet_list)

    for subnet in _subnet_list:
        cmd_message = sh.nordvpn(allowlist_alias, "remove", "subnet", subnet)
        expected_message = MSG_ALLOWLIST_REMOVE_SUBNET_SUCCESS % subnet

        assert expected_message in cmd_message, \
            f"Wrong allowlist message.\nExpected: {expected_message}\nGot: {cmd_message}"

        assert sh.nordvpn.settings().count(subnet) == 0, \
            f"Subnet found in `nordvpn settings`"

        # If subnet /32 whitelisted, only IP Address is visible in `ip route`
        if "/32" in subnet:
            subnet = subnet.replace("/32", "")

        if not _is_private_subnet(subnet):
            assert subnet not in sh.ip.route.show.table(firewall.IP_ROUTE_TABLE), \
                f"Subnet found in `ip route show table {firewall.IP_ROUTE_TABLE}`"