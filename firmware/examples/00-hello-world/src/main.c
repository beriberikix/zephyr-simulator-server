#include <zephyr/kernel.h>
#include <stdio.h>

int main(void)
{
	setvbuf(stdout, NULL, _IONBF, 0);
	setvbuf(stderr, NULL, _IONBF, 0);

	printf("Hello World!\n");
	k_sleep(K_FOREVER);
	return 0;
}
