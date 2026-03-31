#include <zephyr/kernel.h>
#include <stdio.h>

int main(void)
{
	setvbuf(stdout, NULL, _IONBF, 0);
	setvbuf(stderr, NULL, _IONBF, 0);

	printf("Sleep test starting...\n");
	for (int i = 1; i <= 5; i++) {
		int64_t uptime_ms = k_uptime_get();
		printf("[%.3f s] Iteration %d\n", uptime_ms / 1000.0, i);
		k_sleep(K_SECONDS(1));
	}
	printf("Sleep test complete.\n");
	return 0;
}
