"""Verify the Apple certificate that signed a macOS executable."""

import re
import subprocess


def publisher_requirement(team):
    if not re.fullmatch(r"[A-Z0-9]{10}", team):
        raise ValueError("A valid Apple team identifier is required.")
    return (f'anchor apple generic and certificate leaf[subject.OU] = "{team}" '
            'and certificate leaf[field.1.2.840.113635.100.6.1.13] exists')


def execute(args):
    return subprocess.run(args, capture_output=True, text=True, timeout=120)


def verify_signature(binary, team, runner=execute):
    requirement = publisher_requirement(team)
    result = runner(["codesign", "--verify", "--strict", "-R=" + requirement, str(binary)])
    if result.returncode:
        raise ValueError(f"The macOS binary must have a valid Apple Developer ID signature for team {team}. "
                         + result.stderr.strip())
