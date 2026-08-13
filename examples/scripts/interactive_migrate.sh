#!/bin/sh
# A safe stand-in for a migration that has to be answered before it writes.
# It changes nothing; it only asks and reports what it would have done.
set -eu

echo "Pending migrations:"
echo "  - 20260801_add_orders_table"
echo "  - 20260812_backfill_customer_ids"
echo
printf 'Apply these migrations? [y/N] '
read -r answer

case "$answer" in
	y | Y | yes | YES)
		echo "Applying 20260801_add_orders_table ..."
		sleep 1
		echo "Applying 20260812_backfill_customer_ids ..."
		sleep 1
		echo "Done. (This example wrote nothing.)"
		;;
	*)
		echo "Cancelled. Nothing was applied."
		exit 1
		;;
esac
