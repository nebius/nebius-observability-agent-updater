cat << EOF > /etc/apt/keyrings/public-dr-gpg.gpg.pub
-----BEGIN PGP PUBLIC KEY BLOCK-----

mDMEZufxGRYJKwYBBAHaRw8BAQdA6uJ2/wuIidS+bT1cTd0SrH0iHtVdh13neRj+
PpjN0Dy0NU5lYml1cyBTZWN1cml0eSAoUHVibGljIERSIEdQRykgPHNlY3VyaXR5
QG5lYml1cy5jb20+iJkEExYKAEEWIQSZ8Zdte69qddcAoguOYy4tvD4RXgUCZufx
GQIbAwUJBaOagAULCQgHAgIiAgYVCgkICwIEFgIDAQIeBwIXgAAKCRCOYy4tvD4R
XsWIAQDZErUUp/96jcI4uNR7bcG8xrVTBtFZaXW1iVAVJX6qGgEAnxAV6zzWSL/q
UvgvJ3f2nzwpR9Y9L1ydTHKKbRkALw8=
=uHpL
-----END PGP PUBLIC KEY BLOCK-----
EOF
echo "deb [signed-by=/etc/apt/keyrings/public-dr-gpg.gpg.pub] https://dr.nebius.cloud/nebius-observability-agent/ testing main" > /etc/apt/sources.list.d/nebius-observability-agent.list

sudo apt update -o Dir::Etc::sourcelist="sources.list.d/nebius-observability-agent.list" \
    -o Dir::Etc::sourceparts="-" \
    -o APT::Get::List-Cleanup="0"
