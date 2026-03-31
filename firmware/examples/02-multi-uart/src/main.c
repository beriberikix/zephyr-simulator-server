#include <zephyr/kernel.h>
#include <stdio.h>

int main(void)
{
	setvbuf(stdout, NULL, _IONBF, 0);
	setvbuf(stderr, NULL, _IONBF, 0);

	for (int i = 0; i < 5; i++) {
		printf("[UART0] Message %d from UART0\n", i);
		k_msleep(100);
		fprintf(stderr, "[UART1] Message %d from UART1\n", i);
		k_msleep(100);
	}
	return 0;
}
