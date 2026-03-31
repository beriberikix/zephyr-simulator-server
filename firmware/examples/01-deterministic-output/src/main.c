#include <zephyr/kernel.h>
#include <stdio.h>
#include <stdlib.h>

int main(void)
{
	setvbuf(stdout, NULL, _IONBF, 0);
	setvbuf(stderr, NULL, _IONBF, 0);

	for (int i = 0; i < 10; i++) {
		uint32_t rand_val = rand();
		printf("Random value %d: %u\n", i, rand_val);
		k_msleep(100);
	}
	return 0;
}
